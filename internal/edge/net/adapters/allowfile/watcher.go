// SPDX-License-Identifier: MIT

package allowfile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// Watcher is an app.Watcher for the allow list file. It watches the folder, not the file, because
// editors and the store replace the file by renaming a new one over it.
type Watcher struct {
	path string
}

// NewWatcher watches the file at path.
func NewWatcher(path string) *Watcher { return &Watcher{path: filepath.Clean(path)} }

// Watch calls onChange once as soon as the watch is in place, so a change made between the last
// read and the start of the watch is not missed, then after each change to the file until ctx is
// done.
func (w *Watcher) Watch(ctx context.Context, onChange func()) error {
	dir := filepath.Dir(w.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create allow list folder: %w", err)
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watch allow list: %w", err)
	}
	defer func() { _ = fw.Close() }()
	if err := fw.Add(dir); err != nil {
		return fmt.Errorf("watch allow list: %w", err)
	}
	onChange()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-fw.Events:
			if !ok {
				return nil
			}
			if filepath.Clean(ev.Name) == w.path && ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Remove) != 0 {
				onChange()
			}
		case err, ok := <-fw.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("watch allow list: %w", err)
		}
	}
}
