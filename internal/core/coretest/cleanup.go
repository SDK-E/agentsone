// SPDX-License-Identifier: MIT

package coretest

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"testing"
)

// Close closes c and fails the test when Close returns an error, so a test
// never drops a close error with a blank:
//
//	defer coretest.Close(t, f)
func Close(t testing.TB, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

// Check runs a cleanup function and fails the test when it returns an error:
//
//	defer coretest.Check(t, tree.Cleanup)
func Check(t testing.TB, cleanup func() error) {
	t.Helper()
	if err := cleanup(); err != nil {
		t.Errorf("cleanup: %v", err)
	}
}

// Remove removes path and fails the test when it is still there. A path
// that is already gone is fine.
func Remove(t testing.TB, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("remove %s: %v", path, err)
	}
}
