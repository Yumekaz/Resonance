package storage

import "testing"

func TestArtistFolderEvidenceRequiresMatchingCredit(t *testing.T) {
	track := groupingTrack{rootID: "root", albumScope: "Collections/Album One"}
	code, scope := artistScope(track, track.albumScope, "Alex")
	if code != "album_scope" || scope != "root/Collections/Album One" {
		t.Fatalf("generic parent merged an Artist by name: %q %q", code, scope)
	}
	track.albumScope = "Alex/Album One"
	code, scope = artistScope(track, track.albumScope, "Alex")
	if code != "artist_folder" || scope != "root/Alex" {
		t.Fatalf("matching artist folder was not recognized: %q %q", code, scope)
	}
}
