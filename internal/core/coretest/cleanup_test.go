// SPDX-License-Identifier: MIT

package coretest_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// recorder keeps the failures a helper reports instead of failing the test.
type recorder struct {
	testing.TB
	failures []string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

type closer struct{ err error }

func (c closer) Close() error { return c.err }

func TestClose_ReportsACloseFailure(t *testing.T) {
	t.Parallel()
	r := &recorder{TB: t}
	coretest.Close(r, closer{})
	if len(r.failures) != 0 {
		t.Fatalf("clean close reported %v", r.failures)
	}
	coretest.Close(r, closer{err: errors.New("disk full")})
	if len(r.failures) != 1 {
		t.Fatalf("failed close reported %v, want one failure", r.failures)
	}
}

func TestCheck_ReportsAFailingCleanup(t *testing.T) {
	t.Parallel()
	r := &recorder{TB: t}
	coretest.Check(r, func() error { return nil })
	coretest.Check(r, func() error { return errors.New("busy") })
	if len(r.failures) != 1 {
		t.Fatalf("failures = %v, want one", r.failures)
	}
}

func TestRemove_RemovesAndAcceptsAMissingPath(t *testing.T) {
	t.Parallel()
	r := &recorder{TB: t}
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	coretest.Remove(r, file)
	coretest.Remove(r, file)
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file still there: %v", err)
	}
	if len(r.failures) != 0 {
		t.Fatalf("failures = %v, want none", r.failures)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	coretest.Remove(r, dir) // not empty
	if len(r.failures) != 1 {
		t.Fatalf("failures = %v, want one for a folder that is not empty", r.failures)
	}
}
