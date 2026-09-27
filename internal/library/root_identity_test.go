package library

import "testing"

func TestRootIdentityRequiresNonzeroBirthEvidence(t *testing.T) {
	for _, token := range [][]byte{nil, {}, {0, 0, 0, 0}} {
		if hasStrongBirthEvidence(token) {
			t.Fatalf("weak birth evidence accepted: %v", token)
		}
	}
	if !hasStrongBirthEvidence([]byte{0, 0, 1, 0}) {
		t.Fatal("nonzero birth evidence rejected")
	}
}
