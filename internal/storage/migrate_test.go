package storage

import (
	"crypto/sha256"
	"testing"
	"testing/fstest"
)

func TestMigrationOrderingAndChecksums(t *testing.T) {
	for _, names := range [][]string{{"0002_only.sql"}, {"0001_a.sql", "0003_gap.sql"}, {"0001_a.sql", "0001_duplicate.sql"}} {
		files := fstest.MapFS{}
		for _, name := range names {
			files["migrations/"+name] = &fstest.MapFile{Data: []byte("SELECT 1;")}
		}
		if _, err := loadMigrations(files); err == nil {
			t.Fatalf("accepted ordering %v", names)
		}
	}
	files := fstest.MapFS{"migrations/0001_first.sql": {Data: []byte("SELECT 1;\n")}, "migrations/0002_second.sql": {Data: []byte("SELECT 2;\n")}}
	got, err := loadMigrations(files)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].hash != sha256.Sum256(files["migrations/0001_first.sql"].Data) {
		t.Fatal("checksum is not exact file SHA-256")
	}
}
