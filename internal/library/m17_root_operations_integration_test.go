//go:build integration

package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"resonance/internal/storage"
	"testing"
)

func TestM17PhysicalRootOfflineReturnReplacementAndVerification(t *testing.T) {
	s, pool, _ := isolatedLibraryStore(t)
	ctx := context.Background()
	dir := testWorkspaceDir(t)
	offline := dir + "-m17-offline"
	t.Cleanup(func() { _ = os.RemoveAll(offline) })
	writeFile(t, filepath.Join(dir, "retained.wav"), fixtureWAV())
	root := addRoot(t, s, dir, "M17 physical root")
	first, err := testScanner(s).Scan(ctx, root.ID)
	if err != nil || !first.AbsenceReconciled {
		t.Fatal(err)
	}
	locations := locationsForRoot(t, pool, root.ID)
	track := locations[0].TrackID
	if err := s.SetFavorite(ctx, track, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddQueueItem(ctx, "00000000-0000-4000-8000-000000000217", storage.QueueAddRequest{TrackID: track, Placement: "end", ExpectedVersion: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO playback_sessions(id,track_id,client_instance_id,meaningful_at,listened_ms,started_at) VALUES('00000000-0000-4000-8000-000000000218',$1,'00000000-0000-4000-8000-000000000219',now(),30000,now()-interval '40 seconds')`, track); err != nil {
		t.Fatal(err)
	}
	before := m17ProductDigest(t, pool)
	if err := os.Rename(dir, offline); err != nil {
		t.Fatal(err)
	}
	lost, err := testScanner(s).Scan(ctx, root.ID)
	if err == nil || lost.AbsenceReconciled || lost.ObservationsApplied {
		t.Fatal("offline root gained authority")
	}
	if m17ProductDigest(t, pool) != before {
		t.Fatal("offline root altered catalog")
	}
	if err := os.Rename(offline, dir); err != nil {
		t.Fatal(err)
	}
	returned, err := testScanner(s).Scan(ctx, root.ID)
	if err != nil || !returned.AbsenceReconciled || returned.FilesHashed != 0 {
		t.Fatal("matching root did not recover", err)
	}
	if err := os.Rename(dir, offline); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	replacement, err := testScanner(s).Scan(ctx, root.ID)
	if !errors.Is(err, storage.ErrRootIdentityMismatch) || replacement.AbsenceReconciled || replacement.ObservationsApplied {
		t.Fatal("replacement root published", err)
	}
	state, err := s.GetRoot(ctx, root.ID)
	if err != nil || state.VerificationState != "quarantined" || state.LastSuccessfulScanID == nil || *state.LastSuccessfulScanID != returned.RunID {
		t.Fatal("replacement fence state", err)
	}
	current := locationsForRoot(t, pool, root.ID)
	if len(current) != 1 || current[0].ID != locations[0].ID || current[0].Availability != "available" {
		t.Fatal("replacement inferred absence")
	}
	identity, ok, err := CaptureRootIdentity(dir)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := s.VerifyRootIdentity(ctx, root.ID, identity); err != nil {
		t.Fatal(err)
	}
	verified, err := testScanner(s).Scan(ctx, root.ID)
	if err != nil || !verified.AbsenceReconciled || verified.LocationsUnavailable != 1 {
		t.Fatal("explicit verified replacement did not reconcile", err)
	}
	favorites, err := s.ListFavorites(ctx, 50, nil, "")
	if err != nil || len(favorites) != 1 || favorites[0].TrackID != track {
		t.Fatal("favorite intent lost", err)
	}
	queue, err := s.ReadQueue(ctx)
	if err != nil || len(queue.Items) != 1 || queue.Items[0].Available {
		t.Fatal("queue intent lost", err)
	}
	history, err := s.ListHistory(ctx, 50, nil, "")
	if err != nil || len(history) != 1 || history[0].TrackID != track {
		t.Fatal("history lost", err)
	}
	t.Log("actual root object moved away and returned; replacement quarantined without absence; explicit host verification gated later absence; queue/favorite/history retained")
}
