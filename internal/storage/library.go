package storage

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var ErrRootOverlap = errors.New("library root overlaps an enrolled root")
var ErrRootNotFound = errors.New("library root not found")
var ErrRootDisabled = errors.New("library root is disabled")
var ErrScanRunning = errors.New("scan already running for this root")

const enrollmentLockKey int64 = 0x6c6962726f6f7473
const scanWorkerLockKey int64 = 0x7363616e776f726b

type LibraryRoot struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	CanonicalPath string `json:"-"`
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func newLogicalID(prefix string) (string, error) {
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	return prefix + strings.ReplaceAll(id, "-", ""), nil
}

func pathKey(path string) string {
	clean := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

func pathsOverlap(a, b string) bool {
	a, b = pathKey(a), pathKey(b)
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))) {
			return true
		}
	}
	return false
}

func (s *Store) AddRoot(ctx context.Context, name, canonicalPath string) (LibraryRoot, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || canonicalPath == "" {
		return LibraryRoot{}, errors.New("invalid root configuration")
	}
	id, err := newUUID()
	if err != nil {
		return LibraryRoot{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LibraryRoot{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", enrollmentLockKey); err != nil {
		return LibraryRoot{}, err
	}
	rows, err := tx.Query(ctx, "SELECT canonical_path FROM library_roots")
	if err != nil {
		return LibraryRoot{}, err
	}
	for rows.Next() {
		var existing string
		if err := rows.Scan(&existing); err != nil {
			rows.Close()
			return LibraryRoot{}, err
		}
		if pathsOverlap(existing, canonicalPath) {
			rows.Close()
			return LibraryRoot{}, ErrRootOverlap
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return LibraryRoot{}, err
	}
	rows.Close()
	_, err = tx.Exec(ctx, "INSERT INTO library_roots(id,name,canonical_path,path_key) VALUES($1,$2,$3,$4)", id, name, canonicalPath, pathKey(canonicalPath))
	if err != nil {
		return LibraryRoot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LibraryRoot{}, err
	}
	return LibraryRoot{ID: id, Name: name, Enabled: true, CanonicalPath: canonicalPath}, nil
}

func (s *Store) ListRoots(ctx context.Context) ([]LibraryRoot, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text,name,enabled FROM library_roots ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []LibraryRoot{}
	for rows.Next() {
		var root LibraryRoot
		if err := rows.Scan(&root.ID, &root.Name, &root.Enabled); err != nil {
			return nil, err
		}
		result = append(result, root)
	}
	return result, rows.Err()
}

func (s *Store) GetRoot(ctx context.Context, id string) (LibraryRoot, error) {
	if !validUUID(id) {
		return LibraryRoot{}, ErrRootNotFound
	}
	var root LibraryRoot
	err := s.pool.QueryRow(ctx, "SELECT id::text,name,enabled,canonical_path FROM library_roots WHERE id=$1", id).Scan(&root.ID, &root.Name, &root.Enabled, &root.CanonicalPath)
	if errors.Is(err, pgx.ErrNoRows) {
		return LibraryRoot{}, ErrRootNotFound
	}
	return root, err
}

func (s *Store) DisableRoot(ctx context.Context, id string) error {
	if !validUUID(id) {
		return ErrRootNotFound
	}
	tag, err := s.pool.Exec(ctx, "UPDATE library_roots SET enabled=false WHERE id=$1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRootNotFound
	}
	return nil
}

func validUUID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

type ImportMetadata struct {
	Title             string
	TitleSource       string
	ArtistCredit      *string
	AlbumTitle        *string
	AlbumArtistCredit *string
	TrackNumber       *int
	DiscNumber        *int
	Year              *int
	Genre             *string
	ArtworkSHA256     *[32]byte
	ArtworkMIME       *string
}

type ImportFile struct {
	RootID       string
	RelativePath string
	LocalPath    string
	Format       string
	Size         int64
	SHA256       [32]byte
	Metadata     ImportMetadata
}

func (s *Store) LocationExists(ctx context.Context, rootID, relativePath string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM media_locations WHERE root_id=$1 AND relative_path=$2)", rootID, relativePath).Scan(&exists)
	return exists, err
}

// Import inserts a new catalog identity only for unseen encoded bytes. Hashing
// and metadata extraction happen before this short transaction.
func (s *Store) Import(ctx context.Context, file ImportFile) (bool, error) {
	if !validUUID(file.RootID) || file.RelativePath == "" || file.LocalPath == "" || file.Size < 0 {
		return false, errors.New("invalid import")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	lockKey := int64(binary.BigEndian.Uint64(file.SHA256[:8]))
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
		return false, err
	}
	var objectID string
	err = tx.QueryRow(ctx, "SELECT id FROM media_objects WHERE sha256=$1", file.SHA256[:]).Scan(&objectID)
	if errors.Is(err, pgx.ErrNoRows) {
		trackID, e := newLogicalID("trk_")
		if e != nil {
			return false, e
		}
		// Credits and the raw album title are observations on this Track. Artist
		// and Album grouping identities are deliberately deferred to M1.4 so
		// identical display strings are neither falsely merged nor falsely split.
		_, err = tx.Exec(ctx, `INSERT INTO tracks(id,title,artist_credit,album_title,album_artist_credit,track_number,disc_number,release_year,genre,title_source)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, trackID, file.Metadata.Title, file.Metadata.ArtistCredit, file.Metadata.AlbumTitle, file.Metadata.AlbumArtistCredit, file.Metadata.TrackNumber, file.Metadata.DiscNumber, file.Metadata.Year, file.Metadata.Genre, file.Metadata.TitleSource)
		if err != nil {
			return false, err
		}
		objectID = "obj_" + hex.EncodeToString(file.SHA256[:])
		var artHash []byte
		if file.Metadata.ArtworkSHA256 != nil {
			artHash = file.Metadata.ArtworkSHA256[:]
		}
		_, err = tx.Exec(ctx, "INSERT INTO media_objects(id,track_id,sha256,format,byte_length,artwork_sha256,artwork_mime) VALUES($1,$2,$3,$4,$5,$6,$7)", objectID, trackID, file.SHA256[:], file.Format, file.Size, artHash, file.Metadata.ArtworkMIME)
		if err != nil {
			return false, err
		}
	} else if err != nil {
		return false, err
	}
	locationID, err := newLogicalID("loc_")
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, locationID, objectID, file.LocalPath, file.RootID, file.RelativePath)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) BeginScan(ctx context.Context, rootID string) (*ScanLease, string, error) {
	root, err := s.GetRoot(ctx, rootID)
	if err != nil {
		return nil, "", err
	}
	if !root.Enabled {
		return nil, "", ErrRootDisabled
	}
	pooled, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, "", err
	}
	conn := pooled.Hijack()
	lease := &ScanLease{conn: conn}
	var locked bool
	if err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", scanWorkerLockKey).Scan(&locked); err != nil {
		lease.Close()
		return nil, "", err
	}
	if !locked {
		lease.Close()
		return nil, "", ErrScanRunning
	}
	// The global worker lock proves no scan is currently active, so any running
	// row belongs to an interrupted prior process, regardless of root.
	if _, err = s.pool.Exec(ctx, "UPDATE scan_runs SET status='failed',error_code='interrupted',finished_at=now() WHERE status='running'"); err != nil {
		lease.Close()
		return nil, "", err
	}
	runID, err := newUUID()
	if err != nil {
		lease.Close()
		return nil, "", err
	}
	if _, err = s.pool.Exec(ctx, "INSERT INTO scan_runs(id,root_id,status) VALUES($1,$2,'running')", runID, rootID); err != nil {
		lease.Close()
		return nil, "", err
	}
	return lease, runID, nil
}

type ScanLease struct{ conn *pgx.Conn }

func (l *ScanLease) Close() {
	if l == nil || l.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = l.conn.Close(ctx)
	l.conn = nil
}

type ScanCounts struct {
	FilesVisited        int64 `json:"files_visited"`
	FilesSupported      int64 `json:"files_supported"`
	Imported            int64 `json:"imported"`
	Skipped             int64 `json:"skipped"`
	Failed              int64 `json:"failed"`
	BytesHashed         int64 `json:"bytes_hashed"`
	MetadataExtractions int64 `json:"metadata_extractions"`
}

func (s *Store) FinishScan(ctx context.Context, runID, status, errorCode string, counts ScanCounts) error {
	_, err := s.pool.Exec(ctx, `UPDATE scan_runs SET status=$2,error_code=NULLIF($3,''),files_visited=$4,files_supported=$5,imported=$6,skipped=$7,failed=$8,bytes_hashed=$9,metadata_extractions=$10,finished_at=now() WHERE id=$1`, runID, status, errorCode, counts.FilesVisited, counts.FilesSupported, counts.Imported, counts.Skipped, counts.Failed, counts.BytesHashed, counts.MetadataExtractions)
	return err
}

func (s *Store) RecordScanError(ctx context.Context, runID, relativePath, code string) error {
	relativePath = truncateText(strings.ToValidUTF8(relativePath, "�"), 512)
	_, err := s.pool.Exec(ctx, "INSERT INTO scan_errors(run_id,relative_path,code) VALUES($1,$2,$3)", runID, relativePath, code)
	return err
}

func truncateText(value string, maxRunes int) string {
	if maxRunes < 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	seen := 0
	for index := range value {
		if seen == maxRunes {
			return value[:index]
		}
		seen++
	}
	return value
}
