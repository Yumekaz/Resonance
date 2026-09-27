//go:build windows

package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestM16RealWindowsDirectoryWatcher(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	watcher, err := newOSDirectoryWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if err := watcher.Add(root); err != nil {
		t.Fatal("real Windows root watch registration failed")
	}
	if err := watcher.Add(nested); err != nil {
		t.Fatal("real Windows nested watch registration failed")
	}
	if err := os.WriteFile(filepath.Join(nested, "new-track.wav"), []byte("watcher evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-watcher.Events():
			if !ok {
				t.Fatal("real Windows watcher closed before delivering an event")
			}
			if filepath.Base(event.Name) == "new-track.wav" && event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				return
			}
		case err, ok := <-watcher.Errors():
			if ok && err != nil {
				t.Fatal("real Windows watcher reported an error")
			}
		case <-deadline.C:
			t.Fatal("real Windows watcher did not observe the nested file change")
		}
	}
}
