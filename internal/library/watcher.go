package library

import (
	"errors"
	"github.com/fsnotify/fsnotify"
)

// DirectoryWatcher intentionally exposes only directory registration and the
// adapter's two streams. The coordinator is the sole reader of both streams.
type DirectoryWatcher interface {
	Add(string) error
	Remove(string) error
	Close() error
	Events() <-chan fsnotify.Event
	Errors() <-chan error
}

type osDirectoryWatcher struct{ watcher *fsnotify.Watcher }

func newOSDirectoryWatcher() (DirectoryWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &osDirectoryWatcher{watcher: watcher}, nil
}

func (w *osDirectoryWatcher) Add(path string) error         { return w.watcher.Add(path) }
func (w *osDirectoryWatcher) Remove(path string) error      { return w.watcher.Remove(path) }
func (w *osDirectoryWatcher) Close() error                  { return w.watcher.Close() }
func (w *osDirectoryWatcher) Events() <-chan fsnotify.Event { return w.watcher.Events }
func (w *osDirectoryWatcher) Errors() <-chan error          { return w.watcher.Errors }

var errWatcherOverflow = fsnotify.ErrEventOverflow

func isWatcherOverflow(err error) bool { return errors.Is(err, errWatcherOverflow) }
