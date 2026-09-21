package library

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
}

type ScanResult struct {
	RunID     string `json:"run_id"`
	RootID    string `json:"root_id"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
	storage.ScanCounts
	DurationMS     float64 `json:"duration_ms"`
	errorsRecorded int64
	entriesVisited int64
}

func (s *Scanner) Scan(ctx context.Context, rootID string) (result ScanResult, finalErr error) {
	started := time.Now()
	result.RootID = rootID
	rootRecord, err := s.Store.GetRoot(ctx, rootID)
	if err != nil {
		return result, err
	}
	lease, runID, err := s.Store.BeginScan(ctx, rootID)
	if err != nil {
		return result, err
	}
	defer lease.Close()
	result.RunID = runID
	result.Status = "running"
	log := s.Log
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	log.Info("scan_started", "run_id", runID, "root_id", rootID)
	defer func() {
		result.DurationMS = float64(time.Since(started).Microseconds()) / 1000
		if result.Status == "running" {
			switch {
			case errors.Is(ctx.Err(), context.Canceled):
				result.Status = "canceled"
				result.ErrorCode = "canceled"
			case finalErr != nil:
				result.Status = "failed"
				result.ErrorCode = "scan_failed"
			case result.Failed > 0:
				result.Status = "partial"
			default:
				result.Status = "succeeded"
			}
		}
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Store.FinishScan(finishCtx, runID, result.Status, result.ErrorCode, result.ScanCounts); err != nil {
			result.Status = "failed"
			result.ErrorCode = "database_unavailable"
			finalErr = errors.New("database failure during scan finalization")
		}
		log.Info("scan_finished", "run_id", runID, "root_id", rootID, "status", result.Status, "error_code", result.ErrorCode, "files_visited", result.FilesVisited, "files_supported", result.FilesSupported, "imported", result.Imported, "skipped", result.Skipped, "failed", result.Failed, "bytes_hashed", result.BytesHashed, "metadata_extractions", result.MetadataExtractions, "duration_ms", result.DurationMS)
	}()
	if !rootRecord.Enabled {
		result.Status = "failed"
		result.ErrorCode = "root_disabled"
		return result, storage.ErrRootDisabled
	}
	root, err := os.OpenRoot(rootRecord.CanonicalPath)
	if err != nil {
		result.Status = "failed"
		result.ErrorCode = "root_unavailable"
		return result, errors.New("library root unavailable")
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() {
		result.Status = "failed"
		result.ErrorCode = "root_unavailable"
		return result, errors.New("library root unavailable")
	}
	if err := s.walk(ctx, root, rootRecord, ".", 0, &result, log); err != nil {
		if errors.Is(err, context.Canceled) {
			result.Status = "canceled"
			result.ErrorCode = "canceled"
			return result, err
		}
		result.Status = "failed"
		result.ErrorCode = "scan_failed"
		if errors.Is(err, errDatabase) {
			result.ErrorCode = "database_unavailable"
		}
		return result, err
	}
	return result, nil
}

var errDatabase = errors.New("database unavailable during scan")
var errLimit = errors.New("scan traversal limit exceeded")

func (s *Scanner) walk(ctx context.Context, root *os.Root, cfg storage.LibraryRoot, dir string, depth int, result *ScanResult, log *slog.Logger) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > maxDepth {
		return errLimit
	}
	d, err := root.Open(dir)
	if err != nil {
		return s.recordFileError(ctx, result, log, dir, "directory_unavailable", err)
	}
	defer d.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := d.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return s.recordFileError(ctx, result, log, dir, "directory_unavailable", err)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := recordTraversalEntry(result); err != nil {
				return err
			}
			rel := filepath.Join(dir, entry.Name())
			info, statErr := root.Lstat(rel)
			if statErr != nil {
				result.FilesVisited++
				if e := s.recordFileError(ctx, result, log, rel, classifyIO(statErr), statErr); e != nil {
					return e
				}
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeIrregular != 0 {
				result.FilesVisited++
				result.Skipped++
				if e := s.recordCode(ctx, result, log, rel, "symlink_skipped"); e != nil {
					return e
				}
				continue
			}
			if info.IsDir() {
				if e := s.walk(ctx, root, cfg, rel, depth+1, result, log); e != nil {
					return e
				}
				continue
			}
			result.FilesVisited++
			if !info.Mode().IsRegular() {
				result.Skipped++
				if e := s.recordCode(ctx, result, log, rel, "not_regular"); e != nil {
					return e
				}
				continue
			}
			if e := s.processFile(ctx, root, cfg, rel, result, log); e != nil {
				return e
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func recordTraversalEntry(result *ScanResult) error {
	result.entriesVisited++
	if result.entriesVisited > maxVisited {
		return errLimit
	}
	return nil
}

func (s *Scanner) processFile(ctx context.Context, root *os.Root, cfg storage.LibraryRoot, rel string, result *ScanResult, log *slog.Logger) error {
	if !utf8.ValidString(rel) {
		return s.recordFileError(ctx, result, log, rel, "unsupported_path_encoding", errors.New("path is not valid UTF-8"))
	}
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(rel), "."))
	if format != "mp3" && format != "flac" && format != "wav" {
		result.Skipped++
		return s.recordCode(ctx, result, log, rel, "unsupported_format")
	}
	result.FilesSupported++
	relativePath := filepath.ToSlash(rel)
	exists, err := s.Store.LocationExists(ctx, cfg.ID, relativePath)
	if err != nil {
		return databaseFailure(ctx)
	}
	if exists {
		result.Skipped++
		return nil
	}
	opener := s.openFile
	if opener == nil {
		opener = func(r *os.Root, name string) (*os.File, error) { return r.Open(name) }
	}
	f, err := opener(root, rel)
	if err != nil {
		return s.recordFileError(ctx, result, log, rel, classifyIO(err), err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return s.recordFileError(ctx, result, log, rel, "file_unavailable", err)
	}
	if format == "wav" {
		if err := validatePCM(f, info.Size()); err != nil {
			return s.recordFileError(ctx, result, log, rel, "malformed_wav", err)
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return s.recordFileError(ctx, result, log, rel, "file_unavailable", err)
	}
	hasher := sha256.New()
	buf := make([]byte, 32*1024)
	var readTotal int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			_, _ = hasher.Write(buf[:n])
			readTotal += int64(n)
			result.BytesHashed += int64(n)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return s.recordFileError(ctx, result, log, rel, "file_unavailable", readErr)
		}
	}
	if readTotal != info.Size() {
		return s.recordFileError(ctx, result, log, rel, "file_changed", errors.New("file size changed"))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return s.recordFileError(ctx, result, log, rel, "file_unavailable", err)
	}
	meta := storage.ImportMetadata{Title: strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)), TitleSource: "filename"}
	if meta.Title == "" {
		meta.Title = "Untitled"
	}
	if format != "wav" {
		result.MetadataExtractions++
		parsed, _, parseErr := metadata.Read(f)
		if parseErr != nil && !errors.Is(parseErr, tag.ErrNoTagsFound) {
			return s.recordFileError(ctx, result, log, rel, "metadata_invalid", parseErr)
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
				h := sha256.Sum256(parsed.Artwork.Data)
				meta.ArtworkSHA256 = &h
				mime := parsed.Artwork.MIME
				meta.ArtworkMIME = &mime
			}
		}
	}
	var hash [32]byte
	copy(hash[:], hasher.Sum(nil))
	localPath := filepath.Join(cfg.CanonicalPath, rel)
	imported, err := s.Store.Import(ctx, storage.ImportFile{RootID: cfg.ID, RelativePath: relativePath, LocalPath: localPath, Format: format, Size: readTotal, SHA256: hash, Metadata: meta})
	if err != nil {
		return databaseFailure(ctx)
	}
	if imported {
		result.Imported++
	} else {
		result.Skipped++
	}
	return nil
}

func (s *Scanner) recordFileError(ctx context.Context, result *ScanResult, log *slog.Logger, rel, code string, source error) error {
	result.Failed++
	if err := s.recordCode(ctx, result, log, rel, code); err != nil {
		return err
	}
	return nil
}

func (s *Scanner) recordCode(ctx context.Context, result *ScanResult, log *slog.Logger, rel, code string) error {
	log.Info("scan_file_error", "run_id", result.RunID, "root_id", result.RootID, "code", code)
	if result.errorsRecorded < maxRecordedErrors {
		if err := s.Store.RecordScanError(ctx, result.RunID, filepath.ToSlash(rel), code); err != nil {
			return databaseFailure(ctx)
		}
		result.errorsRecorded++
	}
	return nil
}

func databaseFailure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errDatabase
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
