package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var ErrStaleScan = errors.New("scan observations are stale")

const scanObservationTableSQL = `CREATE TEMP TABLE resonance_scan_observations (
	relative_path text PRIMARY KEY,
	path_key text NOT NULL UNIQUE,
	local_path text NOT NULL,
	entry_type text NOT NULL,
	existing_location_id text,
	existing_media_object_id text,
	existing_track_id text,
	observed_size bigint,
	observed_mtime_ns bigint,
	native_id_kind text,
	native_id_scope text,
	native_id bytea,
	native_birth_token bytea,
	sha256 bytea,
	format text,
	title text,
	title_source text,
	artist_credit text,
	album_title text,
	album_artist_credit text,
	track_number integer,
	disc_number integer,
	release_year integer,
	genre text,
	artwork_sha256 bytea,
	artwork_mime text,
	outcome text NOT NULL,
	error_code text
) ON COMMIT PRESERVE ROWS`

type NativeIdentity struct {
	Kind       string
	Scope      string
	ID         []byte
	BirthToken []byte
}

type ScanObservation struct {
	RelativePath          string
	PathKey               string
	LocalPath             string
	RootID                string
	EntryType             string
	ExistingLocationID    *string
	ExistingMediaObjectID *string
	ExistingTrackID       *string
	Size                  *int64
	MTimeNS               *int64
	Native                *NativeIdentity
	SHA256                *[32]byte
	Format                string
	Metadata              ImportMetadata
	Outcome               string
	ErrorCode             string
}

type CatalogLocation struct {
	ID            string
	RelativePath  string
	PathKey       string
	LocalPath     string
	MediaObjectID string
	TrackID       string
	SHA256        [32]byte
	ObservedSize  *int64
	ObservedMTime *int64
	Native        *NativeIdentity
	LastSeenRunID *string
}

type CatalogObject struct {
	ID                       string
	TrackID                  string
	SHA256                   [32]byte
	Format                   string
	ByteLength               int64
	MetadataSourceLocationID *string
}

type CatalogSnapshot struct {
	LastSuccessfulScanID  *string
	Locations             []CatalogLocation
	ObjectsBySHA          map[[32]byte]CatalogObject
	ObjectsByID           map[string]CatalogObject
	MetadataSourceByTrack map[string]*string
	LocationsByPathKey    map[string][]CatalogLocation
}

type scanQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Store) LoadCatalogSnapshot(ctx context.Context, rootID string) (CatalogSnapshot, error) {
	return loadCatalogSnapshot(ctx, s.pool, rootID)
}

func loadCatalogSnapshot(ctx context.Context, q scanQueryer, rootID string) (CatalogSnapshot, error) {
	var snapshot CatalogSnapshot
	snapshot.ObjectsBySHA = make(map[[32]byte]CatalogObject)
	snapshot.ObjectsByID = make(map[string]CatalogObject)
	snapshot.MetadataSourceByTrack = make(map[string]*string)
	snapshot.LocationsByPathKey = make(map[string][]CatalogLocation)
	if err := q.QueryRow(ctx, `SELECT last_successful_scan_id::text FROM library_roots WHERE id=$1`, rootID).Scan(&snapshot.LastSuccessfulScanID); err != nil {
		return CatalogSnapshot{}, err
	}
	rows, err := q.Query(ctx, `SELECT ml.id,ml.relative_path,ml.local_path,ml.media_object_id,mo.track_id,mo.sha256,
		ml.observed_size,ml.observed_mtime_ns,ml.native_id_kind,ml.native_id_scope,ml.native_id,ml.native_birth_token,ml.last_seen_run_id::text
		FROM media_locations ml JOIN media_objects mo ON mo.id=ml.media_object_id
		WHERE ml.root_id=$1 AND ml.availability='available' ORDER BY ml.relative_path,ml.id`, rootID)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	for rows.Next() {
		var loc CatalogLocation
		var hash []byte
		var nativeKind, nativeScope *string
		var nativeID, birthToken []byte
		if err := rows.Scan(&loc.ID, &loc.RelativePath, &loc.LocalPath, &loc.MediaObjectID, &loc.TrackID, &hash,
			&loc.ObservedSize, &loc.ObservedMTime, &nativeKind, &nativeScope, &nativeID, &birthToken, &loc.LastSeenRunID); err != nil {
			rows.Close()
			return CatalogSnapshot{}, err
		}
		if len(hash) != sha256.Size {
			rows.Close()
			return CatalogSnapshot{}, ErrStaleScan
		}
		copy(loc.SHA256[:], hash)
		loc.PathKey = pathKey(loc.RelativePath)
		if nativeKind != nil && nativeScope != nil && nativeID != nil {
			loc.Native = &NativeIdentity{Kind: *nativeKind, Scope: *nativeScope, ID: bytes.Clone(nativeID), BirthToken: bytes.Clone(birthToken)}
		}
		snapshot.Locations = append(snapshot.Locations, loc)
		snapshot.LocationsByPathKey[loc.PathKey] = append(snapshot.LocationsByPathKey[loc.PathKey], loc)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return CatalogSnapshot{}, err
	}
	rows.Close()

	rows, err = q.Query(ctx, `SELECT mo.id,mo.track_id,mo.sha256,mo.format,mo.byte_length,t.metadata_source_location_id::text
		FROM media_objects mo JOIN tracks t ON t.id=mo.track_id ORDER BY mo.sha256`)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var object CatalogObject
		var hash []byte
		if err := rows.Scan(&object.ID, &object.TrackID, &hash, &object.Format, &object.ByteLength, &object.MetadataSourceLocationID); err != nil {
			return CatalogSnapshot{}, err
		}
		if len(hash) != sha256.Size {
			return CatalogSnapshot{}, ErrStaleScan
		}
		copy(object.SHA256[:], hash)
		snapshot.ObjectsBySHA[object.SHA256] = object
		snapshot.ObjectsByID[object.ID] = object
		if _, exists := snapshot.MetadataSourceByTrack[object.TrackID]; !exists {
			snapshot.MetadataSourceByTrack[object.TrackID] = object.MetadataSourceLocationID
		}
	}
	return snapshot, rows.Err()
}

func (l *ScanLease) StageObservation(ctx context.Context, observation ScanObservation) error {
	if l == nil || l.conn == nil || !validRelativeObservationPath(observation.RelativePath) || observation.PathKey == "" || observation.LocalPath == "" || observation.Outcome == "" {
		return errors.New("invalid scan observation")
	}
	var nativeKind, nativeScope string
	var nativeID, birth []byte
	if observation.Native != nil {
		nativeKind, nativeScope = observation.Native.Kind, observation.Native.Scope
		nativeID, birth = observation.Native.ID, observation.Native.BirthToken
	}
	var shaValue, artHash []byte
	if observation.SHA256 != nil {
		shaValue = observation.SHA256[:]
	}
	if observation.Metadata.ArtworkSHA256 != nil {
		artHash = observation.Metadata.ArtworkSHA256[:]
	}
	_, err := l.conn.Exec(ctx, `INSERT INTO resonance_scan_observations(
		relative_path,path_key,local_path,entry_type,existing_location_id,existing_media_object_id,existing_track_id,
		observed_size,observed_mtime_ns,native_id_kind,native_id_scope,native_id,native_birth_token,sha256,format,
		title,title_source,artist_credit,album_title,album_artist_credit,track_number,disc_number,release_year,genre,
		artwork_sha256,artwork_mime,outcome,error_code)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),NULLIF($11,''),$12,$13,$14,NULLIF($15,''),
		NULLIF($16,''),NULLIF($17,''),$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,NULLIF($28,''))`,
		observation.RelativePath, observation.PathKey, observation.LocalPath, observation.EntryType,
		observation.ExistingLocationID, observation.ExistingMediaObjectID, observation.ExistingTrackID,
		observation.Size, observation.MTimeNS, nativeKind, nativeScope, nativeID, birth, shaValue, observation.Format,
		observation.Metadata.Title, observation.Metadata.TitleSource, observation.Metadata.ArtistCredit,
		observation.Metadata.AlbumTitle, observation.Metadata.AlbumArtistCredit, observation.Metadata.TrackNumber,
		observation.Metadata.DiscNumber, observation.Metadata.Year, observation.Metadata.Genre,
		artHash, observation.Metadata.ArtworkMIME, observation.Outcome, observation.ErrorCode)
	return err
}

func (l *ScanLease) InvalidateObservation(ctx context.Context, relativePath, outcome, errorCode string) error {
	if l == nil || l.conn == nil || !validRelativeObservationPath(relativePath) || (outcome != "transient" && outcome != "unstable") {
		return errors.New("invalid scan observation invalidation")
	}
	tag, err := l.conn.Exec(ctx, `UPDATE resonance_scan_observations SET outcome=$2,error_code=NULLIF($3,'') WHERE relative_path=$1`, relativePath, outcome, errorCode)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleScan
	}
	return nil
}

func validRelativeObservationPath(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../") && !strings.HasPrefix(clean, "/")
}

func (l *ScanLease) StagedObservations(ctx context.Context) ([]ScanObservation, error) {
	if l == nil || l.conn == nil {
		return nil, errors.New("scan lease is closed")
	}
	rows, err := l.conn.Query(ctx, `SELECT relative_path,path_key,local_path,entry_type,existing_location_id,existing_media_object_id,existing_track_id,
		observed_size,observed_mtime_ns,native_id_kind,native_id_scope,native_id,native_birth_token,sha256,coalesce(format,''),
		title,title_source,artist_credit,album_title,album_artist_credit,track_number,disc_number,release_year,genre,
		artwork_sha256,artwork_mime,outcome,coalesce(error_code,'') FROM resonance_scan_observations ORDER BY path_key,relative_path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ScanObservation
	for rows.Next() {
		var o ScanObservation
		var existingLocation, existingObject, existingTrack *string
		var size, mtime *int64
		var nativeKind, nativeScope *string
		var nativeID, birth, hash, artHash []byte
		var title, titleSource, artist, album, albumArtist, genre, artMIME *string
		var trackNumber, discNumber, year *int
		if err := rows.Scan(&o.RelativePath, &o.PathKey, &o.LocalPath, &o.EntryType, &existingLocation, &existingObject, &existingTrack,
			&size, &mtime, &nativeKind, &nativeScope, &nativeID, &birth, &hash, &o.Format,
			&title, &titleSource, &artist, &album, &albumArtist, &trackNumber, &discNumber, &year, &genre,
			&artHash, &artMIME, &o.Outcome, &o.ErrorCode); err != nil {
			return nil, err
		}
		o.ExistingLocationID, o.ExistingMediaObjectID, o.ExistingTrackID = existingLocation, existingObject, existingTrack
		o.Size, o.MTimeNS = size, mtime
		if nativeKind != nil && nativeScope != nil && nativeID != nil {
			o.Native = &NativeIdentity{Kind: *nativeKind, Scope: *nativeScope, ID: bytes.Clone(nativeID), BirthToken: bytes.Clone(birth)}
		}
		o.RootID = l.rootID
		if len(hash) != 0 {
			if len(hash) != sha256.Size {
				return nil, ErrStaleScan
			}
			var value [32]byte
			copy(value[:], hash)
			o.SHA256 = &value
		}
		if title != nil {
			o.Metadata.Title = *title
		}
		if titleSource != nil {
			o.Metadata.TitleSource = *titleSource
		}
		o.Metadata.ArtistCredit, o.Metadata.AlbumTitle, o.Metadata.AlbumArtistCredit = artist, album, albumArtist
		o.Metadata.TrackNumber, o.Metadata.DiscNumber, o.Metadata.Year, o.Metadata.Genre, o.Metadata.ArtworkMIME = trackNumber, discNumber, year, genre, artMIME
		if len(artHash) != 0 {
			if len(artHash) != sha256.Size {
				return nil, ErrStaleScan
			}
			var value [32]byte
			copy(value[:], artHash)
			o.Metadata.ArtworkSHA256 = &value
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

func (s *Store) MarkScanPublishing(ctx context.Context, runID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE scan_runs SET phase='publishing' WHERE id=$1 AND status='running' AND phase='discovering'`, runID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleScan
	}
	return nil
}

func (s *Store) PublishScan(ctx context.Context, rootID, runID string, expectedGeneration *string, observations []ScanObservation, traversalComplete bool, counts ScanCounts) (ScanCounts, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return counts, err
	}
	defer tx.Rollback(ctx)
	var enabled bool
	var currentGeneration *string
	if err := tx.QueryRow(ctx, `SELECT enabled,last_successful_scan_id::text FROM library_roots WHERE id=$1 FOR UPDATE`, rootID).Scan(&enabled, &currentGeneration); err != nil {
		return counts, err
	}
	if !enabled {
		return counts, ErrRootDisabled
	}
	if !sameStringPtr(expectedGeneration, currentGeneration) {
		return counts, ErrStaleScan
	}
	var runRoot, status, phase string
	if err := tx.QueryRow(ctx, `SELECT root_id::text,status,phase FROM scan_runs WHERE id=$1 FOR UPDATE`, runID).Scan(&runRoot, &status, &phase); err != nil {
		return counts, err
	}
	if runRoot != rootID || status != "running" || phase != "publishing" {
		return counts, ErrStaleScan
	}
	snapshot, err := loadCatalogSnapshot(ctx, tx, rootID)
	if err != nil {
		return counts, err
	}
	plan, seenIDs, unavailableIDs, err := resolveScanObservations(snapshot, observations)
	if err != nil {
		return counts, err
	}
	for id, reason := range unavailableIDs {
		changed, err := setUnavailable(ctx, tx, id, reason)
		if err != nil {
			return counts, err
		}
		if changed {
			counts.LocationsUnavailable++
		}
	}
	for _, item := range plan {
		if err := applyScanDecision(ctx, tx, item, &counts); err != nil {
			return counts, err
		}
	}
	if traversalComplete {
		seenIDs, err = collectSeenLocationIDs(ctx, tx, rootID, observations, seenIDs)
		if err != nil {
			return counts, err
		}
		for _, loc := range snapshot.Locations {
			if _, seen := seenIDs[loc.ID]; seen {
				continue
			}
			changed, err := setUnavailable(ctx, tx, loc.ID, "not_found")
			if err != nil {
				return counts, err
			}
			if changed {
				counts.LocationsUnavailable++
			}
		}
		ids := make([]string, 0, len(seenIDs))
		for id := range seenIDs {
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE media_locations SET last_seen_run_id=$1,state_updated_at=now()
				WHERE id=ANY($2::text[]) AND root_id=$3 AND availability='available'`, runID, ids, rootID); err != nil {
				return counts, err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE library_roots SET last_successful_scan_id=$2 WHERE id=$1`, rootID, runID); err != nil {
			return counts, err
		}
	}
	counts.TraversalComplete = traversalComplete
	counts.ObservationsApplied = true
	counts.AbsenceReconciled = traversalComplete
	statusValue := "succeeded"
	if !traversalComplete || counts.Failed > 0 {
		statusValue = "partial"
	}
	_, err = tx.Exec(ctx, `UPDATE scan_runs SET status=$2,phase='finished',error_code=NULLIF($3,''),
		files_visited=$4,files_supported=$5,imported=$6,skipped=$7,failed=$8,bytes_hashed=$9,metadata_extractions=$10,
		files_unchanged=$11,files_hashed=$12,stat_changed_same_bytes=$13,changed_bytes=$14,locations_added=$15,
		locations_moved=$16,locations_unavailable=$17,media_objects_created=$18,tracks_created=$19,
		traversal_complete=$20,observations_applied=true,absence_reconciled=$21,finished_at=now()
		WHERE id=$1`, runID, statusValue, partialErrorCode(statusValue, traversalComplete), counts.FilesVisited,
		counts.FilesSupported, counts.Imported, counts.Skipped, counts.Failed, counts.BytesHashed, counts.MetadataExtractions,
		counts.FilesUnchanged, counts.FilesHashed, counts.StatChangedSameBytes, counts.ChangedBytes, counts.LocationsAdded,
		counts.LocationsMoved, counts.LocationsUnavailable, counts.MediaObjectsCreated, counts.TracksCreated,
		traversalComplete, traversalComplete)
	if err != nil {
		return counts, err
	}
	if err := tx.Commit(ctx); err != nil {
		if resolved, ok := s.readPublishedRun(ctx, rootID, runID); ok {
			resolved.TraversalComplete = traversalComplete
			resolved.ObservationsApplied = true
			resolved.AbsenceReconciled = traversalComplete
			return resolved, nil
		}
		return counts, err
	}
	return counts, nil
}

func partialErrorCode(status string, traversalComplete bool) string {
	if status == "partial" && !traversalComplete {
		return "traversal_incomplete"
	}
	return ""
}

func (s *Store) readPublishedRun(ctx context.Context, rootID, runID string) (ScanCounts, bool) {
	var c ScanCounts
	var status, phase string
	var rootRun *string
	err := s.pool.QueryRow(ctx, `SELECT r.status,r.phase,r.traversal_complete,r.observations_applied,r.absence_reconciled,
		r.files_visited,r.files_supported,r.imported,r.skipped,r.failed,r.bytes_hashed,r.metadata_extractions,
		r.files_unchanged,r.files_hashed,r.stat_changed_same_bytes,r.changed_bytes,r.locations_added,r.locations_moved,
		r.locations_unavailable,r.media_objects_created,r.tracks_created,lr.last_successful_scan_id::text
		FROM scan_runs r JOIN library_roots lr ON lr.id=r.root_id WHERE r.id=$1 AND r.root_id=$2`, runID, rootID).Scan(
		&status, &phase, &c.TraversalComplete, &c.ObservationsApplied, &c.AbsenceReconciled,
		&c.FilesVisited, &c.FilesSupported, &c.Imported, &c.Skipped, &c.Failed, &c.BytesHashed, &c.MetadataExtractions,
		&c.FilesUnchanged, &c.FilesHashed, &c.StatChangedSameBytes, &c.ChangedBytes, &c.LocationsAdded, &c.LocationsMoved,
		&c.LocationsUnavailable, &c.MediaObjectsCreated, &c.TracksCreated, &rootRun)
	committed := err == nil && phase == "finished" && status != "running" && c.ObservationsApplied
	if c.AbsenceReconciled {
		committed = committed && rootRun != nil && *rootRun == runID
	}
	return c, committed
}

type scanDecision struct {
	Observation ScanObservation
	Existing    *CatalogLocation
	Object      *CatalogObject
	UseTrackID  string
	CreateTrack bool
	CreateMedia bool
	PreserveID  bool
	Moved       bool
	StatSameSHA bool
	SourceEdit  bool
	Changed     bool
}

func resolveScanObservations(snapshot CatalogSnapshot, observations []ScanObservation) ([]scanDecision, map[string]struct{}, map[string]string, error) {
	oldNative := make(map[string][]CatalogLocation)
	currentNative := make(map[string][]int)
	for _, loc := range snapshot.Locations {
		if loc.Native != nil && snapshot.LastSuccessfulScanID != nil && sameStringPtr(loc.LastSeenRunID, snapshot.LastSuccessfulScanID) {
			key := nativeKey(loc.Native)
			oldNative[key] = append(oldNative[key], loc)
		}
	}
	for i, o := range observations {
		if o.Native != nil && o.SHA256 != nil && o.Outcome == "hashed" {
			key := nativeKey(o.Native)
			currentNative[key] = append(currentNative[key], i)
			continue
		}
		if o.Outcome == "unchanged" {
			known := snapshot.LocationsByPathKey[o.PathKey]
			if len(known) == 1 && known[0].Native != nil {
				key := nativeKey(known[0].Native)
				currentNative[key] = append(currentNative[key], i)
			}
		}
	}
	nativeMatches := make(map[int]CatalogLocation)
	for key, indices := range currentNative {
		old := oldNative[key]
		if len(indices) != 1 || len(old) != 1 {
			continue
		}
		i := indices[0]
		prior := old[0]
		observation := observations[i]
		if observation.Outcome != "hashed" || observation.SHA256 == nil {
			continue
		}
		birthCompatible := len(prior.Native.BirthToken) == 0 && len(observation.Native.BirthToken) == 0 ||
			len(prior.Native.BirthToken) > 0 && len(observation.Native.BirthToken) > 0 && bytes.Equal(prior.Native.BirthToken, observation.Native.BirthToken)
		unchangedBytes := prior.SHA256 == *observation.SHA256
		if birthCompatible && (unchangedBytes || len(prior.Native.BirthToken) > 0) {
			nativeMatches[i] = prior
		}
	}
	plan := make([]scanDecision, 0, len(observations))
	seen := make(map[string]struct{})
	unavailable := make(map[string]string)
	for i, o := range observations {
		knownValues := snapshot.LocationsByPathKey[o.PathKey]
		if len(knownValues) > 1 {
			return nil, nil, nil, fmt.Errorf("%w: ambiguous active path", ErrStaleScan)
		}
		var known *CatalogLocation
		if len(knownValues) == 1 {
			value := knownValues[0]
			known = &value
			seen[value.ID] = struct{}{}
		}
		if known != nil {
			switch o.Outcome {
			case "unreadable":
				unavailable[known.ID] = "unreadable"
				continue
			case "content_invalid":
				unavailable[known.ID] = "content_invalid"
				continue
			case "unsupported":
				unavailable[known.ID] = "unsupported"
				continue
			case "symlink":
				unavailable[known.ID] = "symlink"
				continue
			case "irregular":
				unavailable[known.ID] = "irregular"
				continue
			}
		}
		if o.Outcome == "transient" || o.Outcome == "unstable" {
			continue
		}
		if o.Outcome == "unchanged" {
			if known == nil || known.ID != deref(o.ExistingLocationID) || o.Size == nil || o.MTimeNS == nil {
				return nil, nil, nil, fmt.Errorf("%w: unchanged observation does not match snapshot", ErrStaleScan)
			}
			object := objectByID(snapshot, known.MediaObjectID)
			if object == nil {
				return nil, nil, nil, fmt.Errorf("%w: known location has no media object", ErrStaleScan)
			}
			d := scanDecision{Observation: o, Existing: known, Object: object, UseTrackID: known.TrackID, PreserveID: true, Moved: known.RelativePath != o.RelativePath}
			plan = append(plan, d)
			continue
		}
		if o.Outcome != "hashed" || o.SHA256 == nil {
			continue
		}
		object, objectExists := snapshot.ObjectsBySHA[*o.SHA256]
		var objectPtr *CatalogObject
		if objectExists {
			objectCopy := object
			objectPtr = &objectCopy
		}
		continuity, hasContinuity := nativeMatches[i]
		var existing *CatalogLocation
		if known != nil && objectExists && known.MediaObjectID == object.ID {
			if !nativeExplicitlyDifferent(known.Native, o.Native) {
				existing = known
			}
		}
		if existing == nil && hasContinuity && (!objectExists || continuity.TrackID == object.TrackID) {
			existingCopy := continuity
			existing = &existingCopy
		}
		if existing != nil {
			seen[existing.ID] = struct{}{}
			if known != nil && known.ID != existing.ID {
				unavailable[known.ID] = "replaced"
			}
			trackID := existing.TrackID
			createMedia := !objectExists
			if createMedia && !hasContinuity {
				return nil, nil, nil, fmt.Errorf("%w: changed media cannot inherit Track without native continuity", ErrStaleScan)
			}
			if objectExists {
				trackID = object.TrackID
			}
			if o.Size == nil || o.MTimeNS == nil {
				return nil, nil, nil, fmt.Errorf("%w: hashed observation lacks stat data", ErrStaleScan)
			}
			statChanged := known != nil && known.ID == existing.ID && known.SHA256 == *o.SHA256 && known.ObservedSize != nil && known.ObservedMTime != nil && (*known.ObservedSize != *o.Size || *known.ObservedMTime != *o.MTimeNS)
			d := scanDecision{Observation: o, Existing: existing, Object: objectPtr, UseTrackID: trackID, CreateMedia: createMedia, PreserveID: true, Moved: existing.PathKey != o.PathKey || existing.RelativePath != o.RelativePath, StatSameSHA: statChanged, SourceEdit: createMedia && objectTrackSource(snapshot, existing.TrackID, existing.ID), Changed: existing.SHA256 != *o.SHA256}
			plan = append(plan, d)
			continue
		}
		if known != nil {
			unavailable[known.ID] = "replaced"
		}
		trackID := ""
		createTrack := !objectExists
		createMedia := !objectExists
		if objectExists {
			trackID = object.TrackID
		} else {
			trackID = ""
		}
		plan = append(plan, scanDecision{Observation: o, Object: objectPtr, UseTrackID: trackID, CreateTrack: createTrack, CreateMedia: createMedia, Changed: known != nil && known.SHA256 != *o.SHA256})
	}
	return plan, seen, unavailable, nil
}

func objectByID(snapshot CatalogSnapshot, id string) *CatalogObject {
	if object, exists := snapshot.ObjectsByID[id]; exists {
		value := object
		return &value
	}
	return nil
}

func objectTrackSource(snapshot CatalogSnapshot, trackID, locationID string) bool {
	source, exists := snapshot.MetadataSourceByTrack[trackID]
	return exists && source != nil && *source == locationID
}

func collectSeenLocationIDs(ctx context.Context, tx pgx.Tx, rootID string, observations []ScanObservation, seen map[string]struct{}) (map[string]struct{}, error) {
	pathKeys := make(map[string]struct{}, len(observations))
	for _, observation := range observations {
		pathKeys[observation.PathKey] = struct{}{}
	}
	rows, err := tx.Query(ctx, `SELECT id,relative_path FROM media_locations WHERE root_id=$1 AND availability='available'`, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, relativePath string
		if err := rows.Scan(&id, &relativePath); err != nil {
			return nil, err
		}
		if _, ok := pathKeys[pathKey(relativePath)]; ok {
			seen[id] = struct{}{}
		}
	}
	return seen, rows.Err()
}

func nativeKey(native *NativeIdentity) string {
	if native == nil {
		return ""
	}
	return native.Kind + "\x00" + native.Scope + "\x00" + hex.EncodeToString(native.ID)
}

func nativeExplicitlyDifferent(old, current *NativeIdentity) bool {
	if old == nil || current == nil {
		return false
	}
	if nativeKey(old) != nativeKey(current) {
		return true
	}
	return len(old.BirthToken) > 0 && len(current.BirthToken) > 0 && !bytes.Equal(old.BirthToken, current.BirthToken)
}

func sameStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func applyScanDecision(ctx context.Context, tx pgx.Tx, decision scanDecision, counts *ScanCounts) error {
	o := decision.Observation
	if decision.Existing != nil && decision.PreserveID && o.Outcome == "unchanged" {
		if decision.Object == nil || o.Size == nil || o.MTimeNS == nil {
			return fmt.Errorf("%w: unchanged observation is incomplete", ErrStaleScan)
		}
		if err := updateLocationObservation(ctx, tx, decision.Existing.ID, decision.Object.ID, o); err != nil {
			return err
		}
		if decision.Moved {
			counts.LocationsMoved++
		}
		return nil
	}
	if o.SHA256 == nil || o.Size == nil || o.MTimeNS == nil {
		return fmt.Errorf("%w: publishable observation lacks hash or stat data", ErrStaleScan)
	}
	if decision.Existing != nil && decision.PreserveID {
		var object CatalogObject
		if decision.CreateMedia {
			var found bool
			var err error
			object, found, err = getObjectBySHA(ctx, tx, *o.SHA256)
			if err != nil {
				return err
			}
			if found {
				if object.TrackID != decision.UseTrackID {
					return fmt.Errorf("%w: native continuity conflicts with existing Track", ErrStaleScan)
				}
			} else {
				object, err = insertMediaObject(ctx, tx, decision.UseTrackID, o)
				if err != nil {
					return err
				}
				counts.MediaObjectsCreated++
			}
			if decision.SourceEdit {
				if err := updateTrackMetadata(ctx, tx, decision.UseTrackID, o.Metadata); err != nil {
					return err
				}
			}
		} else if decision.Object != nil {
			object = *decision.Object
		} else {
			var found bool
			var err error
			object, found, err = getObjectBySHA(ctx, tx, *o.SHA256)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("%w: reusable media object disappeared", ErrStaleScan)
			}
		}
		if err := updateLocationObservation(ctx, tx, decision.Existing.ID, object.ID, o); err != nil {
			return err
		}
		if decision.Moved {
			counts.LocationsMoved++
		}
		if decision.StatSameSHA {
			counts.StatChangedSameBytes++
		}
		if decision.Changed {
			counts.ChangedBytes++
		}
		return nil
	}
	locationID, err := newLogicalID("loc_")
	if err != nil {
		return err
	}
	var object CatalogObject
	if decision.Object != nil {
		object = *decision.Object
	} else {
		found := false
		object, found, err = getObjectBySHA(ctx, tx, *o.SHA256)
		if err != nil {
			return err
		}
		if found {
			decision.CreateTrack = false
		} else {
			if !decision.CreateTrack {
				return fmt.Errorf("%w: new bytes have no Track continuity", ErrStaleScan)
			}
			trackID, e := newLogicalID("trk_")
			if e != nil {
				return e
			}
			if err := insertTrack(ctx, tx, trackID, locationID, o.Metadata); err != nil {
				return err
			}
			counts.TracksCreated++
			object, err = insertMediaObject(ctx, tx, trackID, o)
			if err != nil {
				return err
			}
			counts.MediaObjectsCreated++
		}
	}
	if err := insertLocation(ctx, tx, locationID, object.ID, o); err != nil {
		return err
	}
	counts.LocationsAdded++
	counts.Imported++
	if decision.Changed {
		counts.ChangedBytes++
	}
	return nil
}

func updateLocationObservation(ctx context.Context, tx pgx.Tx, locationID, objectID string, o ScanObservation) error {
	tag, err := tx.Exec(ctx, `UPDATE media_locations SET media_object_id=$2,local_path=$3,relative_path=$4,
		observed_size=$5,observed_mtime_ns=$6,
		native_id_kind=CASE WHEN $12='unchanged' THEN native_id_kind ELSE NULLIF($7,'') END,
		native_id_scope=CASE WHEN $12='unchanged' THEN native_id_scope ELSE NULLIF($8,'') END,
		native_id=CASE WHEN $12='unchanged' THEN native_id ELSE $9 END,
		native_birth_token=CASE WHEN $12='unchanged' THEN native_birth_token ELSE $10 END,
		availability='available',unavailable_reason=NULL,unavailable_at=NULL,state_updated_at=now()
		WHERE id=$1 AND root_id=$11 AND availability='available'`, locationID, objectID, o.LocalPath, o.RelativePath, o.Size, o.MTimeNS,
		nativeKind(o.Native), nativeScope(o.Native), nativeID(o.Native), nativeBirth(o.Native), o.RootID, o.Outcome)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleScan
	}
	return nil
}

func insertTrack(ctx context.Context, tx pgx.Tx, trackID, sourceLocationID string, metadata ImportMetadata) error {
	_, err := tx.Exec(ctx, `INSERT INTO tracks(id,title,artist_credit,album_title,album_artist_credit,track_number,disc_number,release_year,genre,title_source,metadata_source_location_id)
		VALUES($1,NULLIF($2,''),$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11)`, trackID, metadata.Title, metadata.ArtistCredit,
		metadata.AlbumTitle, metadata.AlbumArtistCredit, metadata.TrackNumber, metadata.DiscNumber, metadata.Year, metadata.Genre, metadata.TitleSource, sourceLocationID)
	return err
}

func updateTrackMetadata(ctx context.Context, tx pgx.Tx, trackID string, metadata ImportMetadata) error {
	_, err := tx.Exec(ctx, `UPDATE tracks SET title=NULLIF($2,''),artist_credit=$3,album_title=$4,album_artist_credit=$5,
		track_number=$6,disc_number=$7,release_year=$8,genre=$9,title_source=NULLIF($10,'') WHERE id=$1`, trackID,
		metadata.Title, metadata.ArtistCredit, metadata.AlbumTitle, metadata.AlbumArtistCredit, metadata.TrackNumber,
		metadata.DiscNumber, metadata.Year, metadata.Genre, metadata.TitleSource)
	return err
}

func insertMediaObject(ctx context.Context, tx pgx.Tx, trackID string, o ScanObservation) (CatalogObject, error) {
	if o.SHA256 == nil {
		return CatalogObject{}, ErrStaleScan
	}
	objectID := "obj_" + hex.EncodeToString(o.SHA256[:])
	var artHash []byte
	if o.Metadata.ArtworkSHA256 != nil {
		artHash = o.Metadata.ArtworkSHA256[:]
	}
	_, err := tx.Exec(ctx, `INSERT INTO media_objects(id,track_id,sha256,format,byte_length,artwork_sha256,artwork_mime)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, objectID, trackID, o.SHA256[:], o.Format, derefInt64(o.Size), artHash, o.Metadata.ArtworkMIME)
	return CatalogObject{ID: objectID, TrackID: trackID, SHA256: *o.SHA256, Format: o.Format, ByteLength: derefInt64(o.Size)}, err
}

func getObjectBySHA(ctx context.Context, tx pgx.Tx, hash [32]byte) (CatalogObject, bool, error) {
	var object CatalogObject
	var encoded []byte
	err := tx.QueryRow(ctx, `SELECT id,track_id,sha256,format,byte_length FROM media_objects WHERE sha256=$1`, hash[:]).Scan(
		&object.ID, &object.TrackID, &encoded, &object.Format, &object.ByteLength)
	if errors.Is(err, pgx.ErrNoRows) {
		return CatalogObject{}, false, nil
	}
	if err != nil {
		return CatalogObject{}, false, err
	}
	if len(encoded) != sha256.Size {
		return CatalogObject{}, false, ErrStaleScan
	}
	copy(object.SHA256[:], encoded)
	return object, true, nil
}

func insertLocation(ctx context.Context, tx pgx.Tx, locationID, objectID string, o ScanObservation) error {
	_, err := tx.Exec(ctx, `INSERT INTO media_locations(id,media_object_id,local_path,root_id,relative_path,availability,
		observed_size,observed_mtime_ns,native_id_kind,native_id_scope,native_id,native_birth_token,state_updated_at)
		VALUES($1,$2,$3,$4,$5,'available',$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11,now())`, locationID, objectID,
		o.LocalPath, o.RootID, o.RelativePath, o.Size, o.MTimeNS, nativeKind(o.Native), nativeScope(o.Native), nativeID(o.Native), nativeBirth(o.Native))
	return err
}

func setUnavailable(ctx context.Context, tx pgx.Tx, locationID, reason string) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE media_locations SET availability='unavailable',unavailable_reason=$2,unavailable_at=now(),state_updated_at=now()
		WHERE id=$1 AND availability='available'`, locationID, reason)
	return tag.RowsAffected() == 1, err
}

func nativeKind(identity *NativeIdentity) string {
	if identity == nil {
		return ""
	}
	return identity.Kind
}

func nativeScope(identity *NativeIdentity) string {
	if identity == nil {
		return ""
	}
	return identity.Scope
}

func nativeID(identity *NativeIdentity) []byte {
	if identity == nil {
		return nil
	}
	return identity.ID
}

func nativeBirth(identity *NativeIdentity) []byte {
	if identity == nil {
		return nil
	}
	return identity.BirthToken
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func (s *Store) ResolveUncertainPublish(ctx context.Context, rootID, runID string) (ScanCounts, bool) {
	return s.readPublishedRun(ctx, rootID, runID)
}
