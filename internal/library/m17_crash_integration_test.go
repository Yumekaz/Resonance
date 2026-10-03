//go:build integration

package library

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"resonance/internal/storage"
)

// A subprocess helper, never a production fault endpoint. The parent owns
// the isolated schema and kills the child at a proven transaction boundary.
func TestM17CrashChild(t *testing.T) {
	mode := os.Getenv("RESONANCE_M17_CRASH_CHILD")
	if mode == "" {
		return
	}
	s, err := storage.Open(context.Background(), os.Getenv("RESONANCE_M17_CHILD_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	marker := os.Getenv("RESONANCE_M17_CHILD_MARKER")
	scanner := testScanner(s)
	if mode == "discovery" {
		scanner.afterTraversal = func() {
			if err := os.WriteFile(marker, []byte("observations_staged"), 0600); err != nil {
				panic(err)
			}
			select {}
		}
	}
	if mode == "dirty" {
		scanner.afterTraversal = func() {
			if _, err := os.Stat(marker + ".gate"); err == nil {
				_ = os.WriteFile(marker, []byte("dirty_scan_staged"), 0600)
				select {}
			}
		}
		c, err := NewCoordinator(CoordinatorOptions{Store: s, Scanner: scanner, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Run(context.Background())
		return
	}
	result, err := scanner.Scan(context.Background(), os.Getenv("RESONANCE_M17_CHILD_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	if mode == "commit_without_delivery" {
		body, _ := json.Marshal(result)
		if err := os.WriteFile(marker, body, 0600); err != nil {
			t.Fatal(err)
		}
		select {}
	}
}

func m17Wait(t *testing.T, condition func() bool) {
	t.Helper()
	until := time.Now().Add(20 * time.Second)
	for time.Now().Before(until) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("M1.7 synchronization boundary not reached")
}
func m17StartChild(t *testing.T, dsn, root, mode, marker string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestM17CrashChild$", "-test.timeout=2m")
	cmd.Env = append(os.Environ(), "RESONANCE_M17_CRASH_CHILD="+mode, "RESONANCE_M17_CHILD_DSN="+dsn+" application_name=m17_crash_child", "RESONANCE_M17_CHILD_ROOT="+root, "RESONANCE_M17_CHILD_MARKER="+marker)
	dir := os.Getenv("RESONANCE_M17_ATTEMPT_OUTPUT")
	if dir == "" {
		dir = t.TempDir()
	}
	f, err := os.OpenFile(filepath.Join(dir, mode+"-child.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		_ = f.Close()
	})
	return cmd
}
func m17Kill(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
}
func m17ProductDigest(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	tables := []string{"tracks", "media_objects", "media_locations", "catalog_artists", "catalog_albums", "track_album_memberships", "track_artist_memberships", "library_roots", "catalog_grouping_state"}
	out := ""
	for _, table := range tables {
		var hash string
		if err := pool.QueryRow(context.Background(), "SELECT md5(COALESCE(string_agg(j,',' ORDER BY j),'')) FROM (SELECT row_to_json(x)::text j FROM "+table+" x) z").Scan(&hash); err != nil {
			t.Fatal(err)
		}
		out += hash
	}
	return out
}

func TestM17PublicationCrashCampaign(t *testing.T) {
	for _, mode := range []string{"discovery", "publication", "commit_without_delivery"} {
		t.Run(mode, func(t *testing.T) {
			s, pool, dsn := isolatedLibraryStore(t)
			rootDir := testWorkspaceDir(t)
			root := addRoot(t, s, rootDir, "M17 crash")
			file := filepath.Join(rootDir, "Artist", "Album", "song.mp3")
			writeFile(t, file, fixtureMP3WithTitle(t, "Before"))
			baseline, err := testScanner(s).Scan(context.Background(), root.ID)
			if err != nil {
				t.Fatal(err)
			}
			before := m17ProductDigest(t, pool)
			writeFile(t, file, fixtureMP3WithTitle(t, "After"))
			marker := filepath.Join(t.TempDir(), "boundary")
			var unlock func()
			if mode == "publication" {
				lock, err := pool.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := lock.Exec(context.Background(), "SELECT pg_advisory_lock(170917)"); err != nil {
					t.Fatal(err)
				}
				unlock = func() { _, _ = lock.Exec(context.Background(), "SELECT pg_advisory_unlock(170917)"); lock.Release() }
				defer unlock()
				if _, err := pool.Exec(context.Background(), `CREATE FUNCTION m17_publication_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.phase='finished' AND NEW.observations_applied THEN PERFORM pg_advisory_xact_lock(170917); END IF; RETURN NEW; END $$; CREATE TRIGGER m17_publication_barrier BEFORE UPDATE ON scan_runs FOR EACH ROW EXECUTE FUNCTION m17_publication_barrier()`); err != nil {
					t.Fatal(err)
				}
			}
			child := m17StartChild(t, dsn, root.ID, mode, marker)
			if mode == "publication" {
				m17Wait(t, func() bool {
					var waiting bool
					_ = pool.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name='m17_crash_child' AND wait_event='advisory')").Scan(&waiting)
					return waiting
				})
			} else {
				m17Wait(t, func() bool { _, err := os.Stat(marker); return err == nil })
			}
			m17Kill(t, child)
			if mode == "publication" {
				if _, err := pool.Exec(context.Background(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name='m17_crash_child'"); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(context.Background(), "DROP TRIGGER m17_publication_barrier ON scan_runs; DROP FUNCTION m17_publication_barrier()"); err != nil {
					t.Fatal(err)
				}
			}
			// Process exit does not synchronously prove that PostgreSQL has
			// observed the closed socket and released its session scan lock.
			// Inspect that boundary before testing the next scanner's recovery.
			m17Wait(t, func() bool {
				var live bool
				err := pool.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name='m17_crash_child')").Scan(&live)
				return err == nil && !live
			})
			if mode != "commit_without_delivery" {
				if got := m17ProductDigest(t, pool); got != before {
					t.Fatal("partial catalog/grouping/generation publication survived crash")
				}
				var phase, status string
				var authoritative bool
				if err := pool.QueryRow(context.Background(), "SELECT phase,status,observations_applied FROM scan_runs WHERE id<>$1 ORDER BY started_at DESC LIMIT 1", baseline.RunID).Scan(&phase, &status, &authoritative); err != nil || status != "running" || authoritative {
					t.Fatal("pre-recovery scan state", phase, status, authoritative, err)
				}
			} else {
				body, err := os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				var result ScanResult
				if err := json.Unmarshal(body, &result); err != nil {
					t.Fatal(err)
				}
				var committed bool
				if err := pool.QueryRow(context.Background(), `SELECT r.observations_applied AND r.absence_reconciled AND r.phase='finished' AND lr.last_successful_scan_id=r.id FROM scan_runs r JOIN library_roots lr ON lr.id=r.root_id WHERE r.id=$1`, result.RunID).Scan(&committed); err != nil || !committed {
					t.Fatal("ambiguous caller outcome missing durable markers", err)
				}
			}
			follow, err := testScanner(s).Scan(context.Background(), root.ID)
			if err != nil || !follow.AbsenceReconciled {
				t.Fatal("recovery did not converge", err)
			}
			var liveTracks int
			if err := pool.QueryRow(context.Background(), "SELECT count(DISTINCT mo.track_id) FROM media_locations ml JOIN media_objects mo ON mo.id=ml.media_object_id WHERE ml.availability='available'").Scan(&liveTracks); err != nil || liveTracks != 1 {
				t.Fatal("recovery duplicated identity", liveTracks, err)
			}
			if mode == "commit_without_delivery" && follow.FilesHashed != 0 {
				t.Fatal("blind replay after committed outcome")
			}
			if mode != "commit_without_delivery" {
				var interrupted int
				_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM scan_runs WHERE error_code='interrupted' AND phase='finished'").Scan(&interrupted)
				if interrupted != 1 {
					t.Fatal("abandoned run not finalized", interrupted)
				}
			}
			t.Logf("%s: pre-retry state inspected; atomic authority/recovery assertions passed", mode)
		})
	}
}

func TestM17DirtyWorkHardCrashAndStartupSweep(t *testing.T) {
	s, pool, dsn := isolatedLibraryStore(t)
	dir := testWorkspaceDir(t)
	root := addRoot(t, s, dir, "M17 dirty recovery")
	marker := filepath.Join(t.TempDir(), "dirty")
	child := m17StartChild(t, dsn, root.ID, "dirty", marker)
	m17Wait(t, func() bool {
		var n int
		_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM scan_runs WHERE status='succeeded'").Scan(&n)
		return n > 0
	})
	_ = os.WriteFile(marker+".gate", []byte("gate"), 0600)
	writeFile(t, filepath.Join(dir, "before-crash.wav"), fixtureWAV())
	m17Wait(t, func() bool { _, err := os.Stat(marker); return err == nil })
	m17Kill(t, child)
	changed := fixtureWAV()
	changed[len(changed)-1] ^= 1
	writeFile(t, filepath.Join(dir, "while-stopped.wav"), changed)
	_, stop, done := startIntegrationCoordinator(t, s)
	defer stop()
	waitLocationState(t, pool, root.ID, "while-stopped.wav", "available")
	waitLocationState(t, pool, root.ID, "before-crash.wav", "available")
	stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("recovery coordinator shutdown")
	}
	var n int
	_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM scan_runs WHERE error_code='interrupted'").Scan(&n)
	if n != 1 {
		t.Fatal("abandoned dirty run not interrupted", n)
	}
}
