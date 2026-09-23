package library

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"resonance/internal/metadata"
	"resonance/internal/storage"

	"github.com/dhowden/tag"
)

const maxDepth = 64
const maxVisited = 100000
const maxRecordedErrors = 100

type Scanner struct {
	Store *storage.Store
	Log   *slog.Logger
	// openFile is a fault-injection seam for filesystem races and permission tests.
	openFile func(*os.Root, string) (*os.File, error)
	// openDir is a fault-injection seam for incomplete traversal tests.
	openDir func(*os.Root, string) (*os.File, error)
	// nativeIdentity can be replaced by a deterministic fake in tests.
	nativeIdentity NativeIdentityProvider
}

type ScanResult struct {
	RunID     string `json:"run_id"`
	RootID    string `json:"root_id"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
	storage.ScanCounts
	DurationMS           float64 `json:"duration_ms"`
	PublishTransactionMS float64 `json:"publish_transaction_ms"`
	SQLStatements        int64   `json:"sql_statements"`
	RowsAffected         int64   `json:"rows_affected"`
	errorsRecorded       int64
	entriesVisited       int64
}

type scanState struct {
	ctx           context.Context
	scanner       *Scanner
	lease         *storage.ScanLease
	root          storage.LibraryRoot
	snap          storage.CatalogSnapshot
	result        *ScanResult
	log           *slog.Logger
	incomplete    bool
	seenSHA       map[[32]byte]struct{}
	metadataBySHA map[[32]byte]storage.ImportMetadata
	fileFences    []fileFence
}

type fileFence struct {
	relativePath string
	info         os.FileInfo
}

func (s *Scanner) Scan(ctx context.Context, rootID string) (result ScanResult, finalErr error) {
	started := time.Now()
	result.RootID = rootID
	workMetrics := storage.NewScanWorkMetrics()
	ctx = storage.ContextWithScanWork(ctx, workMetrics)
	log := s.Log
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	lease, runID, rootRecord, err := s.Store.BeginScan(ctx, rootID)
	if err != nil {
		return result, err
	}
	defer lease.Close()
	result.RunID = runID
	result.Status = "running"
	log.Info("scan_started", "run_id", runID, "root_id", rootID)
	finalized := false
	traversalComplete := false
	defer func() {
		result.DurationMS = float64(time.Since(started).Microseconds()) / 1000
		if result.Status == "running" {
			switch {
			case errors.Is(ctx.Err(), context.Canceled):
				result.Status = "canceled"
				result.ErrorCode = "canceled"
			case finalErr != nil:
				result.Status = "failed"
				if result.ErrorCode == "" {
					result.ErrorCode = "scan_failed"
				}
			case !traversalComplete || result.Failed > 0:
				result.Status = "partial"
			default:
				result.Status = "succeeded"
			}
		}
		if !finalized {
			finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			finishCtx = storage.ContextWithScanWork(finishCtx, workMetrics)
			if err := s.Store.FinishScanWithoutPublish(finishCtx, runID, result.Status, result.ErrorCode, result.ScanCounts, traversalComplete); err != nil {
				result.Status = "failed"
				result.ErrorCode = "database_unavailable"
				finalErr = errors.New("database failure during scan finalization")
			}
		}
		result.SQLStatements, result.RowsAffected = workMetrics.Snapshot()
		log.Info("scan_finished", "run_id", runID, "root_id", rootID, "status", result.Status, "error_code", result.ErrorCode,
			"files_visited", result.FilesVisited, "files_supported", result.FilesSupported, "files_unchanged", result.FilesUnchanged,
			"files_hashed", result.FilesHashed, "imported", result.Imported, "locations_added", result.LocationsAdded,
			"locations_moved", result.LocationsMoved, "locations_unavailable", result.LocationsUnavailable,
			"failed", result.Failed, "bytes_hashed", result.BytesHashed, "metadata_extractions", result.MetadataExtractions,
			"traversal_complete", result.TraversalComplete, "observations_applied", result.ObservationsApplied,
			"absence_reconciled", result.AbsenceReconciled, "duration_ms", result.DurationMS,
			"publish_transaction_ms", result.PublishTransactionMS, "sql_statements", result.SQLStatements, "rows_affected", result.RowsAffected)
	}()

	root, err := os.OpenRoot(rootRecord.CanonicalPath)
	if err != nil {
		result.Status = "failed"
		result.ErrorCode = "root_unavailable"
		finalErr = errors.New("library root unavailable")
		return result, finalErr
	}
	defer root.Close()
	rootBefore, err := root.Stat(".")
	if err != nil || !rootBefore.IsDir() {
		result.Status = "failed"
		result.ErrorCode = "root_unavailable"
		finalErr = errors.New("library root unavailable")
		return result, finalErr
	}
	canonicalBefore, err := os.Stat(rootRecord.CanonicalPath)
	if err != nil || !os.SameFile(rootBefore, canonicalBefore) {
		result.Status = "failed"
		result.ErrorCode = "root_unavailable"
		finalErr = errors.New("library root unavailable")
		return result, finalErr
	}
	snapshot, err := s.Store.LoadCatalogSnapshot(ctx, rootID)
	if err != nil {
		result.Status = "failed"
		result.ErrorCode = "database_unavailable"
		finalErr = databaseFailure(ctx)
		return result, finalErr
	}
	state := &scanState{ctx: ctx, scanner: s, lease: lease, root: rootRecord, snap: snapshot, result: &result, log: log, seenSHA: make(map[[32]byte]struct{}), metadataBySHA: make(map[[32]byte]storage.ImportMetadata)}
	if err := state.walk(ctx, root, ".", 0); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			result.Status = "canceled"
			result.ErrorCode = "canceled"
			finalErr = err
			return result, err
		case errors.Is(err, errDatabase):
			result.Status = "failed"
			result.ErrorCode = "database_unavailable"
			finalErr = err
			return result, err
		case errors.Is(err, errLimit):
			state.incomplete = true
			if recordErr := state.recordFileError(".", "traversal_limit", "transient", nil); recordErr != nil {
				result.Status = "failed"
				result.ErrorCode = "database_unavailable"
				finalErr = recordErr
				return result, recordErr
			}
		default:
			state.incomplete = true
			if recordErr := state.recordFileError(".", "traversal_incomplete", "transient", nil); recordErr != nil {
				result.Status = "failed"
				result.ErrorCode = "database_unavailable"
				finalErr = recordErr
				return result, recordErr
			}
		}
	}
	if err := state.recheckStagedFiles(root); err != nil {
		if errors.Is(err, context.Canceled) {
			result.Status = "canceled"
			result.ErrorCode = "canceled"
		} else {
			result.Status = "failed"
			result.ErrorCode = "database_unavailable"
		}
		finalErr = err
		return result, err
	}
	rootAfter, err := root.Stat(".")
	if err != nil || !rootAfter.IsDir() {
		result.Status = "failed"
		result.ErrorCode = "root_unavailable"
		finalErr = errors.New("library root unavailable")
		return result, finalErr
	}
	canonicalAfter, err := os.Stat(rootRecord.CanonicalPath)
	if err != nil || !os.SameFile(rootBefore, canonicalAfter) || !os.SameFile(rootBefore, rootAfter) {
		result.Status = "failed"
		result.ErrorCode = "root_unavailable"
		finalErr = errors.New("library root changed during scan")
		return result, finalErr
	}
	if !sameDirectoryToken(rootBefore, rootAfter) {
		state.incomplete = true
		if err := state.recordFileError(".", "directory_changed_during_scan", "transient", nil); err != nil {
			result.Status = "failed"
			result.ErrorCode = "database_unavailable"
			finalErr = err
			return result, err
		}
	}
	traversalComplete = !state.incomplete
	result.TraversalComplete = traversalComplete
	if err := ctx.Err(); err != nil {
		result.Status = "canceled"
		result.ErrorCode = "canceled"
		finalErr = err
		return result, err
	}
	if err := s.Store.MarkScanPublishing(ctx, runID); err != nil {
		if ctx.Err() != nil {
			result.Status = "canceled"
			result.ErrorCode = "canceled"
			finalErr = ctx.Err()
		} else {
			result.Status = "failed"
			result.ErrorCode = "database_unavailable"
			finalErr = errDatabase
		}
		return result, finalErr
	}
	observations, err := lease.StagedObservations(ctx)
	if err != nil {
		if ctx.Err() != nil {
			result.Status = "canceled"
			result.ErrorCode = "canceled"
			finalErr = ctx.Err()
		} else {
			result.Status = "failed"
			result.ErrorCode = "database_unavailable"
			finalErr = errDatabase
		}
		return result, finalErr
	}
	publishStarted := time.Now()
	counts, err := s.Store.PublishScan(ctx, rootID, runID, snapshot.LastSuccessfulScanID, observations, traversalComplete, result.ScanCounts)
	result.PublishTransactionMS = float64(time.Since(publishStarted).Microseconds()) / 1000
	if err != nil {
		resolveCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		resolved, published := s.Store.ResolveUncertainPublish(resolveCtx, rootID, runID)
		cancel()
		if published {
			result.ScanCounts = resolved
			finalized = true
			if resolved.AbsenceReconciled && resolved.Failed == 0 {
				result.Status = "succeeded"
			} else {
				result.Status = "partial"
			}
			return result, nil
		}
		if ctx.Err() != nil {
			result.Status = "canceled"
			result.ErrorCode = "canceled"
			finalErr = ctx.Err()
			return result, finalErr
		}
		result.Status = "failed"
		result.ErrorCode = "publish_failed"
		if errors.Is(err, storage.ErrRootDisabled) {
			result.ErrorCode = "root_disabled"
		}
		finalErr = fmt.Errorf("scan observations could not be published: %w", err)
		return result, finalErr
	}
	result.ScanCounts = counts
	finalized = true
	if !traversalComplete || result.Failed > 0 {
		result.Status = "partial"
	} else {
		result.Status = "succeeded"
	}
	return result, nil
}

var errDatabase = errors.New("database unavailable during scan")
var errLimit = errors.New("scan traversal limit exceeded")

func recordTraversalEntry(result *ScanResult) error {
	result.entriesVisited++
	if result.entriesVisited > maxVisited {
		return errLimit
	}
	return nil
}

func (st *scanState) walk(ctx context.Context, root *os.Root, dir string, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > maxDepth {
		return errLimit
	}
	openDir := st.scanner.openDir
	if openDir == nil {
		openDir = func(r *os.Root, name string) (*os.File, error) { return r.Open(name) }
	}
	d, err := openDir(root, dir)
	if err != nil {
		if recordErr := st.recordFileError(dir, "directory_unavailable", "transient", nil); recordErr != nil {
			return recordErr
		}
		return nil
	}
	defer d.Close()
	dirBefore, err := d.Stat()
	if err != nil || !dirBefore.IsDir() {
		if recordErr := st.recordFileError(dir, "directory_unavailable", "transient", nil); recordErr != nil {
			return recordErr
		}
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, readErr := d.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := recordTraversalEntry(st.result); err != nil {
				return err
			}
			rel := entry.Name()
			if dir != "." {
				rel = filepath.Join(dir, entry.Name())
			}
			if !utf8.ValidString(rel) {
				st.incomplete = true
				st.result.FilesVisited++
				if err := st.recordFileError(rel, "unsupported_path_encoding", "transient", nil); err != nil {
					return err
				}
				continue
			}
			info, statErr := root.Lstat(rel)
			if statErr != nil {
				st.result.FilesVisited++
				outcome := "transient"
				code := classifyIO(statErr)
				if errors.Is(statErr, os.ErrPermission) {
					outcome = "unreadable"
				}
				if err := st.recordFileError(rel, code, outcome, nil); err != nil {
					return err
				}
				if outcome == "unreadable" {
					if err := st.stageSimple(rel, "unknown", outcome, code, nil); err != nil {
						return err
					}
				}
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				st.result.FilesVisited++
				st.result.Skipped++
				if err := st.recordCode(rel, "symlink_skipped"); err != nil {
					return err
				}
				if st.hasActiveDescendant(rel) {
					st.incomplete = true
					if err := st.recordFileError(rel, "symlink_subtree_skipped", "symlink", info); err != nil {
						return err
					}
				}
				if err := st.stageSimple(rel, "symlink", "symlink", "", info); err != nil {
					return err
				}
				continue
			}
			if info.Mode()&os.ModeIrregular != 0 {
				st.result.FilesVisited++
				st.result.Skipped++
				if err := st.recordCode(rel, "irregular_skipped"); err != nil {
					return err
				}
				if err := st.stageSimple(rel, "irregular", "irregular", "", info); err != nil {
					return err
				}
				continue
			}
			if info.IsDir() {
				if err := st.walk(ctx, root, rel, depth+1); err != nil {
					return err
				}
				continue
			}
			st.result.FilesVisited++
			if !info.Mode().IsRegular() {
				st.result.Skipped++
				if err := st.recordCode(rel, "not_regular"); err != nil {
					return err
				}
				if err := st.stageSimple(rel, "irregular", "irregular", "", info); err != nil {
					return err
				}
				continue
			}
			if err := st.processFile(ctx, root, rel, info); err != nil {
				return err
			}
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			if err := st.recordFileError(dir, "directory_unavailable", "transient", nil); err != nil {
				return err
			}
			break
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	dirAfter, err := d.Stat()
	pathAfter, pathErr := root.Lstat(dir)
	if err != nil || pathErr != nil || !pathAfter.IsDir() || !sameDirectoryToken(dirBefore, dirAfter) || !os.SameFile(dirBefore, pathAfter) {
		if err := st.recordFileError(dir, "directory_changed_during_scan", "transient", nil); err != nil {
			return err
		}
	}
	return nil
}

func (st *scanState) processFile(ctx context.Context, root *os.Root, rel string, enumInfo os.FileInfo) error {
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(rel), "."))
	if format != "mp3" && format != "flac" && format != "wav" {
		st.result.Skipped++
		if err := st.recordCode(rel, "unsupported_format"); err != nil {
			return err
		}
		return st.stageSimple(rel, "regular", "unsupported", "", enumInfo)
	}
	st.result.FilesSupported++
	known, err := st.knownLocation(rel)
	if err != nil {
		return err
	}
	relativePath := filepath.ToSlash(rel)
	size := enumInfo.Size()
	mtime := enumInfo.ModTime().UnixNano()
	if known != nil && known.ObservedSize != nil && known.ObservedMTime != nil && *known.ObservedSize == size && *known.ObservedMTime == mtime {
		st.result.Skipped++
		st.result.FilesUnchanged++
		st.fileFences = append(st.fileFences, fileFence{relativePath: relativePath, info: enumInfo})
		return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: "regular", Outcome: "unchanged", Size: &size, MTimeNS: &mtime}, known)
	}
	opener := st.scanner.openFile
	if opener == nil {
		opener = func(r *os.Root, name string) (*os.File, error) { return r.Open(name) }
	}
	f, err := opener(root, rel)
	if err != nil {
		outcome := "transient"
		if errors.Is(err, os.ErrPermission) {
			outcome = "unreadable"
			st.incomplete = true
		} else {
			st.incomplete = true
		}
		if recErr := st.recordFileError(rel, classifyIO(err), outcome, enumInfo); recErr != nil {
			return recErr
		}
		return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: "regular", Outcome: outcome, Size: &size, MTimeNS: &mtime}, known)
	}
	defer f.Close()
	openedInfo, err := f.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !sameFileVersion(enumInfo, openedInfo) {
		st.incomplete = true
		if recErr := st.recordFileError(rel, "file_changed_during_scan", "unstable", enumInfo); recErr != nil {
			return recErr
		}
		return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: "regular", Outcome: "unstable", Size: &size, MTimeNS: &mtime}, known)
	}
	var native *storage.NativeIdentity
	provider := st.scanner.nativeIdentity
	if provider == nil {
		provider = systemNativeIdentityProvider()
	}
	if identity, ok, nativeErr := provider.FromOpenFile(f); nativeErr == nil && ok && identity.Kind != "" && identity.Scope != "" && len(identity.ID) != 0 {
		native = &identity
	}
	hasher := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			_, _ = hasher.Write(buf[:n])
			st.result.BytesHashed += int64(n)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			st.incomplete = true
			outcome := "transient"
			if errors.Is(readErr, os.ErrPermission) {
				outcome = "unreadable"
			}
			if recErr := st.recordFileError(rel, classifyIO(readErr), outcome, openedInfo); recErr != nil {
				return recErr
			}
			return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: "regular", Outcome: outcome, Size: &size, MTimeNS: &mtime}, known)
		}
	}
	var hash [32]byte
	copy(hash[:], hasher.Sum(nil))
	postHashInfo, err := f.Stat()
	if err != nil || !sameFileVersion(openedInfo, postHashInfo) {
		st.incomplete = true
		if recErr := st.recordFileError(rel, "file_changed_during_scan", "unstable", openedInfo); recErr != nil {
			return recErr
		}
		return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: "regular", Outcome: "unstable", Size: &size, MTimeNS: &mtime}, known)
	}
	st.result.FilesHashed++
	_, alreadySeen := st.snap.ObjectsBySHA[hash]
	_, stagedDuplicate := st.seenSHA[hash]
	newSHA := !alreadySeen && !stagedDuplicate
	meta := storage.ImportMetadata{Title: strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)), TitleSource: "filename"}
	if meta.Title == "" {
		meta.Title = "Untitled"
	}
	if stagedDuplicate && !alreadySeen {
		if previous, ok := st.metadataBySHA[hash]; ok {
			meta = previous
			if meta.TitleSource == "filename" {
				meta.Title = strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
				if meta.Title == "" {
					meta.Title = "Untitled"
				}
			}
		}
	}
	if format == "wav" && newSHA {
		validationErr := validatePCM(f, openedInfo.Size())
		postProcessInfo, statErr := f.Stat()
		if statErr != nil || !sameFileVersion(openedInfo, postProcessInfo) {
			st.incomplete = true
			if recErr := st.recordFileError(rel, "file_changed_during_scan", "unstable", openedInfo); recErr != nil {
				return recErr
			}
			return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: "regular", Outcome: "unstable", Size: &size, MTimeNS: &mtime}, known)
		}
		if validationErr != nil {
			if recErr := st.recordFileError(rel, "malformed_wav", "content_invalid", openedInfo); recErr != nil {
				return recErr
			}
			return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: "regular", Size: &size, MTimeNS: &mtime, Native: native, SHA256: &hash, Format: format, Outcome: "content_invalid", ErrorCode: "malformed_wav"}, known)
		}
	}
	if format != "wav" && newSHA {
		st.result.MetadataExtractions++
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			st.incomplete = true
			if recErr := st.recordFileError(rel, "file_unavailable", "unreadable", openedInfo); recErr != nil {
				return recErr
			}
			return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: "regular", Outcome: "unreadable", Size: &size, MTimeNS: &mtime}, known)
		}
		parsed, _, parseErr := metadata.Read(f)
		postProcessInfo, statErr := f.Stat()
		if statErr != nil || !sameFileVersion(openedInfo, postProcessInfo) {
			st.incomplete = true
			if recErr := st.recordFileError(rel, "file_changed_during_scan", "unstable", openedInfo); recErr != nil {
				return recErr
			}
			return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: "regular", Outcome: "unstable", Size: &size, MTimeNS: &mtime}, known)
		}
		if parseErr != nil && !errors.Is(parseErr, tag.ErrNoTagsFound) {
			if recErr := st.recordFileError(rel, "metadata_invalid", "content_invalid", openedInfo); recErr != nil {
				return recErr
			}
			return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: "regular", Size: &size, MTimeNS: &mtime, Native: native, SHA256: &hash, Format: format, Outcome: "content_invalid", ErrorCode: "metadata_invalid"}, known)
		}
		if parseErr == nil {
			if parsed.Title != nil {
				meta.Title = *parsed.Title
				meta.TitleSource = "tag"
			}
			meta.ArtistCredit = parsed.Artist
			meta.AlbumTitle = parsed.Album
			meta.AlbumArtistCredit = parsed.AlbumArtist
			meta.TrackNumber = parsed.TrackNumber
			meta.DiscNumber = parsed.DiscNumber
			meta.Year = parsed.Year
			meta.Genre = parsed.Genre
			if parsed.Artwork != nil {
				artHash := sha256.Sum256(parsed.Artwork.Data)
				meta.ArtworkSHA256 = &artHash
				mime := parsed.Artwork.MIME
				meta.ArtworkMIME = &mime
			}
		}
	}
	if newSHA {
		postParseInfo, statErr := f.Stat()
		if statErr != nil || !sameFileVersion(openedInfo, postParseInfo) {
			st.incomplete = true
			if recErr := st.recordFileError(rel, "file_changed_during_scan", "unstable", openedInfo); recErr != nil {
				return recErr
			}
			return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: "regular", Outcome: "unstable", Size: &size, MTimeNS: &mtime}, known)
		}
	}
	if newSHA {
		st.metadataBySHA[hash] = meta
	}
	st.seenSHA[hash] = struct{}{}
	// The open handle can remain stable after its pathname is replaced. The
	// final confined path check must cover hashed files as well as skipped ones.
	st.fileFences = append(st.fileFences, fileFence{relativePath: relativePath, info: openedInfo})
	return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: "regular", Size: &size, MTimeNS: &mtime, Native: native, SHA256: &hash, Format: format, Metadata: meta, Outcome: "hashed"}, known)
}

func (st *scanState) knownLocation(rel string) (*storage.CatalogLocation, error) {
	values := st.snap.LocationsByPathKey[scanPathKey(rel)]
	if len(values) > 1 {
		return nil, storage.ErrStaleScan
	}
	if len(values) == 0 {
		return nil, nil
	}
	value := values[0]
	return &value, nil
}

func (st *scanState) stageSimple(rel, entryType, outcome, errorCode string, info os.FileInfo) error {
	var size, mtime *int64
	if info != nil {
		s, m := info.Size(), info.ModTime().UnixNano()
		size, mtime = &s, &m
	}
	relativePath := filepath.ToSlash(rel)
	if info != nil {
		st.fileFences = append(st.fileFences, fileFence{relativePath: relativePath, info: info})
	}
	return st.stageObservation(storage.ScanObservation{RelativePath: relativePath, PathKey: scanPathKey(rel), LocalPath: filepath.Join(st.root.CanonicalPath, rel), EntryType: entryType, Outcome: outcome, ErrorCode: errorCode, Size: size, MTimeNS: mtime}, nil)
}

func (st *scanState) recheckStagedFiles(root *os.Root) error {
	for _, fence := range st.fileFences {
		if err := st.ctx.Err(); err != nil {
			return err
		}
		current, err := root.Lstat(filepath.FromSlash(fence.relativePath))
		if err == nil && sameEntryVersion(fence.info, current) {
			continue
		}
		outcome, code := "unstable", "file_changed_during_scan"
		if errors.Is(err, os.ErrNotExist) {
			outcome, code = "transient", "file_unavailable"
		} else if errors.Is(err, os.ErrPermission) {
			outcome, code = "transient", "permission_denied"
		}
		st.incomplete = true
		if recordErr := st.recordFileError(filepath.FromSlash(fence.relativePath), code, outcome, fence.info); recordErr != nil {
			return recordErr
		}
		if err := st.lease.InvalidateObservation(st.ctx, fence.relativePath, outcome, code); err != nil {
			return databaseFailure(st.ctx)
		}
	}
	return nil
}

func (st *scanState) stageObservation(observation storage.ScanObservation, known *storage.CatalogLocation) error {
	if known != nil {
		observation.ExistingLocationID = stringPointer(known.ID)
		observation.ExistingMediaObjectID = stringPointer(known.MediaObjectID)
		observation.ExistingTrackID = stringPointer(known.TrackID)
	}
	if err := st.lease.StageObservation(st.ctx, observation); err != nil {
		return databaseFailure(st.ctx)
	}
	return nil
}

func stringPointer(value string) *string { return &value }

func (st *scanState) recordFileError(rel, code, outcome string, info os.FileInfo) error {
	st.result.Failed++
	st.incomplete = st.incomplete || outcome == "transient" || outcome == "unreadable" || outcome == "unstable"
	if err := st.recordCode(rel, code); err != nil {
		return err
	}
	return nil
}

func (st *scanState) recordCode(rel, code string) error {
	st.log.Info("scan_file_error", "run_id", st.result.RunID, "root_id", st.result.RootID, "code", code)
	if st.result.errorsRecorded >= maxRecordedErrors {
		return nil
	}
	if err := st.scanner.Store.RecordScanError(st.ctx, st.result.RunID, filepath.ToSlash(rel), code); err != nil {
		return databaseFailure(st.ctx)
	}
	st.result.errorsRecorded++
	return nil
}

func (st *scanState) hasActiveDescendant(rel string) bool {
	prefix := scanPathKey(rel) + string(filepath.Separator)
	for key := range st.snap.LocationsByPathKey {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func scanPathKey(rel string) string {
	key := filepath.Clean(filepath.FromSlash(rel))
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

func sameDirectoryToken(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func sameFileVersion(a, b os.FileInfo) bool {
	return a != nil && b != nil && a.Mode().IsRegular() && b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().UnixNano() == b.ModTime().UnixNano()
}

func sameEntryVersion(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().UnixNano() == b.ModTime().UnixNano()
}

func classifyIO(err error) string {
	if errors.Is(err, os.ErrPermission) {
		return "permission_denied"
	}
	if errors.Is(err, os.ErrNotExist) {
		return "file_unavailable"
	}
	return "file_unavailable"
}

func databaseFailure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errDatabase
}

// CanonicalizeRoot is only used by host-side CLI enrollment.
func CanonicalizeRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("root path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("invalid root path")
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Some restricted hosts permit opening a workspace path but deny
		// traversing its absolute ancestors. Resolve it relative to cwd too;
		// both paths still require EvalSymlinks to succeed.
		cwd, cwdErr := os.Getwd()
		rel, relErr := filepath.Rel(cwd, abs)
		if cwdErr != nil || relErr != nil || filepath.IsAbs(rel) {
			return "", errors.New("root unavailable")
		}
		resolved, resolveErr := filepath.EvalSymlinks(rel)
		if resolveErr != nil {
			return "", errors.New("root unavailable")
		}
		if filepath.IsAbs(resolved) {
			canonical = resolved
		} else {
			canonical = filepath.Join(cwd, resolved)
		}
	}
	canonical, err = canonicalPathByHandle(canonical)
	if err != nil {
		return "", errors.New("root unavailable")
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return "", errors.New("root unavailable")
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() {
		return "", errors.New("root must be a readable directory")
	}
	d, err := root.Open(".")
	if err != nil {
		return "", errors.New("root is not readable")
	}
	defer d.Close()
	if _, err = d.ReadDir(1); err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("root is not readable")
	}
	return canonical, nil
}
