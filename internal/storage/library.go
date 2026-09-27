package storage

import (
	"bytes"
	"context"
	"crypto/rand"
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
var ErrRootUnverified = errors.New("library root identity is unverified")
var ErrRootQuarantined = errors.New("library root identity is quarantined")
var ErrRootIdentityMismatch = errors.New("library root identity mismatch")
var ErrRootIdentityUnavailable = errors.New("library root identity unavailable")
var ErrRootUnavailable = errors.New("library root unavailable")
var ErrScanRunning = errors.New("scan already running for this root")

const enrollmentLockKey int64 = 0x6c6962726f6f7473
const scanWorkerLockKey int64 = 0x7363616e776f726b

type LibraryRoot struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	Enabled              bool            `json:"enabled"`
	VerificationState    string          `json:"verification_state"`
	VerifiedAt           *time.Time      `json:"verified_at,omitempty"`
	CanonicalPath        string          `json:"-"`
	LastSuccessfulScanID *string         `json:"-"`
	Identity             *NativeIdentity `json:"-"`
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
	return s.addRoot(ctx, name, canonicalPath, nil)
}

func (s *Store) AddRootWithIdentity(ctx context.Context, name, canonicalPath string, identity NativeIdentity) (LibraryRoot, error) {
	if !validRootIdentity(&identity) {
		return LibraryRoot{}, ErrRootIdentityUnavailable
	}
	return s.addRoot(ctx, name, canonicalPath, &identity)
}

func (s *Store) addRoot(ctx context.Context, name, canonicalPath string, identity *NativeIdentity) (LibraryRoot, error) {
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
	verificationState := "unverified"
	var kind, scope any
	var objectID, birthToken []byte
	if identity != nil {
		verificationState = "verified"
		kind, scope = identity.Kind, identity.Scope
		objectID, birthToken = identity.ID, identity.BirthToken
		if len(birthToken) == 0 {
			birthToken = nil
		}
	}
	var hasIdentityColumns bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='library_roots' AND column_name='root_identity_kind')`).Scan(&hasIdentityColumns); err != nil {
		return LibraryRoot{}, err
	}
	if !hasIdentityColumns {
		if identity != nil {
			return LibraryRoot{}, ErrRootIdentityUnavailable
		}
		_, err = tx.Exec(ctx, `INSERT INTO library_roots(id,name,canonical_path,path_key) VALUES($1,$2,$3,$4)`, id, name, canonicalPath, pathKey(canonicalPath))
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO library_roots(id,name,canonical_path,path_key,root_identity_kind,root_identity_scope,root_identity_id,root_identity_birth_token,verification_state,verified_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,CASE WHEN $9='verified' THEN now() END)`, id, name, canonicalPath, pathKey(canonicalPath), kind, scope, objectID, birthToken, verificationState)
	}
	if err != nil {
		return LibraryRoot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LibraryRoot{}, err
	}
	root := LibraryRoot{ID: id, Name: name, Enabled: true, CanonicalPath: canonicalPath, VerificationState: verificationState}
	if identity != nil {
		copyIdentity := cloneRootIdentity(identity)
		root.Identity = &copyIdentity
		now := time.Now().UTC()
		root.VerifiedAt = &now
	}
	return root, nil
}

func (s *Store) ListRoots(ctx context.Context) ([]LibraryRoot, error) {
	roots, err := s.listRoots(ctx)
	if err != nil {
		return nil, err
	}
	for i := range roots {
		roots[i].CanonicalPath = ""
		roots[i].Identity = nil
	}
	return roots, nil
}

// ListRootsForReconciliation returns host-only root bindings for the scanner
// coordinator. Public/CLI root listings use ListRoots, which omits both path
// and native identity evidence.
func (s *Store) ListRootsForReconciliation(ctx context.Context) ([]LibraryRoot, error) {
	return s.listRoots(ctx)
}

func (s *Store) listRoots(ctx context.Context) ([]LibraryRoot, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,name,enabled,canonical_path,last_successful_scan_id::text,
		root_identity_kind,root_identity_scope,root_identity_id,root_identity_birth_token,verification_state,verified_at
		FROM library_roots ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []LibraryRoot{}
	for rows.Next() {
		root, err := scanLibraryRoot(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, root)
	}
	return result, rows.Err()
}

// ListRootsAfter returns a bounded page ordered by stable root ID. The
// coordinator uses it to advance in-memory full-sweep cursors without
// retaining a path-bearing or unbounded work queue.
func (s *Store) ListRootsAfter(ctx context.Context, afterID string, limit int) ([]LibraryRoot, error) {
	if limit < 1 || limit > 256 {
		return nil, errors.New("invalid root page size")
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,name,enabled,canonical_path,last_successful_scan_id::text,
		root_identity_kind,root_identity_scope,root_identity_id,root_identity_birth_token,verification_state,verified_at
		FROM library_roots WHERE id::text > $1 ORDER BY id::text LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]LibraryRoot, 0, limit)
	for rows.Next() {
		root, err := scanLibraryRoot(rows)
		if err != nil {
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
	root, err := scanLibraryRoot(s.pool.QueryRow(ctx, `SELECT id::text,name,enabled,canonical_path,last_successful_scan_id::text,
		root_identity_kind,root_identity_scope,root_identity_id,root_identity_birth_token,verification_state,verified_at
		FROM library_roots WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return LibraryRoot{}, ErrRootNotFound
	}
	return root, err
}

type libraryRootScanner interface{ Scan(...any) error }

func scanLibraryRoot(row libraryRootScanner) (LibraryRoot, error) {
	var root LibraryRoot
	var identityKind, identityScope *string
	var identityID, birthToken []byte
	err := row.Scan(&root.ID, &root.Name, &root.Enabled, &root.CanonicalPath, &root.LastSuccessfulScanID,
		&identityKind, &identityScope, &identityID, &birthToken, &root.VerificationState, &root.VerifiedAt)
	if err != nil {
		return LibraryRoot{}, err
	}
	if identityKind != nil && identityScope != nil && identityID != nil {
		root.Identity = &NativeIdentity{Kind: *identityKind, Scope: *identityScope, ID: bytes.Clone(identityID), BirthToken: bytes.Clone(birthToken)}
	}
	return root, nil
}

func validRootIdentity(identity *NativeIdentity) bool {
	return identity != nil && len(identity.Kind) > 0 && len(identity.Kind) <= 64 && len(identity.Scope) > 0 && len(identity.Scope) <= 256 && len(identity.ID) > 0 && len(identity.ID) <= 256 && len(identity.BirthToken) <= 256
}

func cloneRootIdentity(identity *NativeIdentity) NativeIdentity {
	return NativeIdentity{Kind: identity.Kind, Scope: identity.Scope, ID: bytes.Clone(identity.ID), BirthToken: bytes.Clone(identity.BirthToken)}
}

func sameRootIdentity(a, b *NativeIdentity) bool {
	return validRootIdentity(a) && validRootIdentity(b) && a.Kind == b.Kind && a.Scope == b.Scope && bytes.Equal(a.ID, b.ID) && bytes.Equal(a.BirthToken, b.BirthToken)
}

func RootIdentityEqual(a, b *NativeIdentity) bool { return sameRootIdentity(a, b) }

func (s *Store) VerifyRootIdentity(ctx context.Context, id string, identity NativeIdentity) (LibraryRoot, error) {
	if !validUUID(id) {
		return LibraryRoot{}, ErrRootNotFound
	}
	if !validRootIdentity(&identity) {
		return LibraryRoot{}, ErrRootIdentityUnavailable
	}
	if len(identity.BirthToken) == 0 {
		identity.BirthToken = nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LibraryRoot{}, err
	}
	defer tx.Rollback(ctx)
	root, err := scanLibraryRoot(tx.QueryRow(ctx, `SELECT id::text,name,enabled,canonical_path,last_successful_scan_id::text,
		root_identity_kind,root_identity_scope,root_identity_id,root_identity_birth_token,verification_state,verified_at
		FROM library_roots WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return LibraryRoot{}, ErrRootNotFound
	}
	if err != nil {
		return LibraryRoot{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE library_roots SET root_identity_kind=$2,root_identity_scope=$3,root_identity_id=$4,
		root_identity_birth_token=$5,verification_state='verified',verified_at=now() WHERE id=$1`, id, identity.Kind, identity.Scope, identity.ID, identity.BirthToken); err != nil {
		return LibraryRoot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LibraryRoot{}, err
	}
	copyIdentity := cloneRootIdentity(&identity)
	root.Identity = &copyIdentity
	root.VerificationState = "verified"
	now := time.Now().UTC()
	root.VerifiedAt = &now
	return root, nil
}

func (s *Store) EnableRoot(ctx context.Context, id string, current NativeIdentity) (LibraryRoot, error) {
	if !validUUID(id) {
		return LibraryRoot{}, ErrRootNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LibraryRoot{}, err
	}
	defer tx.Rollback(ctx)
	root, err := scanLibraryRoot(tx.QueryRow(ctx, `SELECT id::text,name,enabled,canonical_path,last_successful_scan_id::text,
		root_identity_kind,root_identity_scope,root_identity_id,root_identity_birth_token,verification_state,verified_at
		FROM library_roots WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return LibraryRoot{}, ErrRootNotFound
	}
	if err != nil {
		return LibraryRoot{}, err
	}
	if root.VerificationState == "unverified" {
		return LibraryRoot{}, ErrRootUnverified
	}
	if root.VerificationState == "quarantined" || !sameRootIdentity(root.Identity, &current) {
		if root.VerificationState == "verified" {
			if _, err = tx.Exec(ctx, "UPDATE library_roots SET verification_state='quarantined' WHERE id=$1", id); err != nil {
				return LibraryRoot{}, err
			}
			if err = tx.Commit(ctx); err != nil {
				return LibraryRoot{}, err
			}
		}
		return LibraryRoot{}, ErrRootIdentityMismatch
	}
	if _, err = tx.Exec(ctx, "UPDATE library_roots SET enabled=true WHERE id=$1", id); err != nil {
		return LibraryRoot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LibraryRoot{}, err
	}
	root.Enabled = true
	return root, nil
}

func (s *Store) QuarantineRootIfIdentity(ctx context.Context, id string, expected NativeIdentity) (bool, error) {
	if !validUUID(id) || !validRootIdentity(&expected) {
		return false, ErrRootIdentityMismatch
	}
	tag, err := s.pool.Exec(ctx, `UPDATE library_roots SET verification_state='quarantined'
		WHERE id=$1 AND verification_state='verified' AND root_identity_kind=$2 AND root_identity_scope=$3
		AND root_identity_id=$4 AND root_identity_birth_token IS NOT DISTINCT FROM $5`, id, expected.Kind, expected.Scope, expected.ID, expected.BirthToken)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
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

func (s *Store) BeginScan(ctx context.Context, rootID string) (*ScanLease, string, LibraryRoot, error) {
	if !validUUID(rootID) {
		return nil, "", LibraryRoot{}, ErrRootNotFound
	}
	pooled, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, "", LibraryRoot{}, err
	}
	conn := pooled.Hijack()
	lease := &ScanLease{conn: conn, rootID: rootID}
	var locked bool
	if err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", scanWorkerLockKey).Scan(&locked); err != nil {
		lease.Close()
		return nil, "", LibraryRoot{}, err
	}
	if !locked {
		lease.Close()
		return nil, "", LibraryRoot{}, ErrScanRunning
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		lease.Close()
		return nil, "", LibraryRoot{}, err
	}
	defer tx.Rollback(ctx)
	// The global worker lock proves no scan is currently active, so any running
	// row belongs to an interrupted prior process, regardless of root.
	if _, err = tx.Exec(ctx, "UPDATE scan_runs SET status='failed',phase='finished',error_code='interrupted',finished_at=now() WHERE status='running'"); err != nil {
		lease.Close()
		return nil, "", LibraryRoot{}, err
	}
	root, err := scanLibraryRoot(tx.QueryRow(ctx, `SELECT id::text,name,enabled,canonical_path,last_successful_scan_id::text,
		root_identity_kind,root_identity_scope,root_identity_id,root_identity_birth_token,verification_state,verified_at
		FROM library_roots WHERE id=$1`, rootID))
	if errors.Is(err, pgx.ErrNoRows) {
		lease.Close()
		return nil, "", LibraryRoot{}, ErrRootNotFound
	}
	if err != nil {
		lease.Close()
		return nil, "", LibraryRoot{}, err
	}
	if !root.Enabled {
		lease.Close()
		return nil, "", LibraryRoot{}, ErrRootDisabled
	}
	if root.VerificationState == "unverified" || root.Identity == nil {
		lease.Close()
		return nil, "", LibraryRoot{}, ErrRootUnverified
	}
	if root.VerificationState == "quarantined" {
		lease.Close()
		return nil, "", LibraryRoot{}, ErrRootQuarantined
	}
	runID, err := newUUID()
	if err != nil {
		lease.Close()
		return nil, "", LibraryRoot{}, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO scan_runs(id,root_id,status,phase) VALUES($1,$2,'running','discovering')", runID, rootID); err != nil {
		lease.Close()
		return nil, "", LibraryRoot{}, err
	}
	if _, err = tx.Exec(ctx, scanObservationTableSQL); err != nil {
		lease.Close()
		return nil, "", LibraryRoot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		lease.Close()
		return nil, "", LibraryRoot{}, err
	}
	return lease, runID, root, nil
}

type ScanLease struct {
	conn   *pgx.Conn
	rootID string
}

func (l *ScanLease) Close() {
	if l == nil || l.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = l.conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", scanWorkerLockKey)
	_ = l.conn.Close(ctx)
	l.conn = nil
}

type ScanCounts struct {
	GroupingMS           float64 `json:"grouping_ms"`
	FilesVisited         int64   `json:"files_visited"`
	FilesSupported       int64   `json:"files_supported"`
	Imported             int64   `json:"imported"`
	Skipped              int64   `json:"skipped"`
	Failed               int64   `json:"failed"`
	BytesHashed          int64   `json:"bytes_hashed"`
	MetadataExtractions  int64   `json:"metadata_extractions"`
	FilesUnchanged       int64   `json:"files_unchanged"`
	FilesHashed          int64   `json:"files_hashed"`
	StatChangedSameBytes int64   `json:"stat_changed_same_bytes"`
	ChangedBytes         int64   `json:"changed_bytes"`
	LocationsAdded       int64   `json:"locations_added"`
	LocationsMoved       int64   `json:"locations_moved"`
	LocationsUnavailable int64   `json:"locations_unavailable"`
	MediaObjectsCreated  int64   `json:"media_objects_created"`
	TracksCreated        int64   `json:"tracks_created"`
	TraversalComplete    bool    `json:"traversal_complete"`
	ObservationsApplied  bool    `json:"observations_applied"`
	AbsenceReconciled    bool    `json:"absence_reconciled"`
}

func (s *Store) FinishScanWithoutPublish(ctx context.Context, runID, status, errorCode string, counts ScanCounts, traversalComplete bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE scan_runs SET status=$2,phase='finished',error_code=NULLIF($3,''),files_visited=$4,files_supported=$5,imported=$6,skipped=$7,failed=$8,bytes_hashed=$9,metadata_extractions=$10,files_unchanged=$11,files_hashed=$12,stat_changed_same_bytes=$13,changed_bytes=$14,locations_added=$15,locations_moved=$16,locations_unavailable=$17,media_objects_created=$18,tracks_created=$19,traversal_complete=$20,observations_applied=false,absence_reconciled=false,finished_at=now() WHERE id=$1 AND status='running'`, runID, status, errorCode, counts.FilesVisited, counts.FilesSupported, counts.Imported, counts.Skipped, counts.Failed, counts.BytesHashed, counts.MetadataExtractions, counts.FilesUnchanged, counts.FilesHashed, counts.StatChangedSameBytes, counts.ChangedBytes, counts.LocationsAdded, counts.LocationsMoved, counts.LocationsUnavailable, counts.MediaObjectsCreated, counts.TracksCreated, traversalComplete)
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
