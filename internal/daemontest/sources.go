// SPDX-License-Identifier: MIT

package daemontest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// builtinSources is how many built-in event sources the events module runs
// (clock, disk, load and files). MoveSources waits for each of them to be
// asleep.
const builtinSources = 4

// inboxDir is the folder the files source watches, next to config.toml.
const inboxDir = "inbox"

// DropFile writes a file named name into the folder the files source
// watches. The source announces it once two scans in a row saw it unchanged.
func (d *Daemon) DropFile(name, text string) {
	d.t.Helper()
	d.sourcesAsleep()
	path := filepath.Join(filepath.Dir(d.setup.cfgPath), inboxDir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		d.t.Fatalf("drop %s: %v", name, err)
	}
}

// mountsFile is the disk source's mount table, next to config.toml.
const mountsFile = "mounts"

// procDir is the load source's /proc, next to config.toml.
const procDir = "proc"

// The load source's meminfo: half free, and almost full.
const (
	memoryHalfFree = "MemTotal: 1000000 kB\nMemAvailable: 500000 kB\n"
	memoryFull     = "MemTotal: 1000000 kB\nMemAvailable: 10000 kB\n"
)

// writeProc writes the load source's stat, with an idle CPU that never
// moves, and meminfo into dir.
func writeProc(t testing.TB, dir, meminfo string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("make the load source's proc folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte("cpu 0 0 0 1000 0 0 0 0 0 0\n"), 0o600); err != nil {
		t.Fatalf("write stat: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte(meminfo), 0o600); err != nil {
		t.Fatalf("write meminfo: %v", err)
	}
}

// FillMemory makes the load source's next sample see 99 percent of memory
// in use.
func (d *Daemon) FillMemory() {
	d.t.Helper()
	d.sourcesAsleep()
	writeProc(d.t, filepath.Join(filepath.Dir(d.setup.cfgPath), procDir), memoryFull)
}

// sourceSleeper makes the built-in event sources wait on the daemon's fake
// clock: a sleep ends once the clock has reached its end. Only the test
// moves the clock for them, with MoveSources.
type sourceSleeper struct {
	clock *coretest.Clock

	mu sync.Mutex
	// moved is closed, and replaced, each time MoveSources moves the clock.
	moved chan struct{}
	// ends holds the end of each sleep in progress, by sleep.
	ends map[int]time.Time
	next int
}

func newSourceSleeper(c *coretest.Clock) *sourceSleeper {
	return &sourceSleeper{clock: c, moved: make(chan struct{}), ends: map[int]time.Time{}}
}

// Sleep waits until the fake clock has moved by d, or until ctx ends.
func (s *sourceSleeper) Sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	end := s.clock.Now().Add(d)
	id := s.next
	s.next++
	s.ends[id] = end
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.ends, id)
		s.mu.Unlock()
	}()
	for {
		s.mu.Lock()
		moved := s.moved
		s.mu.Unlock()
		if !s.clock.Now().Before(end) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-moved:
		}
	}
}

// asleep counts the sleeps that are not over yet.
func (s *sourceSleeper) asleep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	n := 0
	for _, end := range s.ends {
		if now.Before(end) {
			n++
		}
	}
	return n
}

// move moves the clock by by and wakes every sleep that is over.
func (s *sourceSleeper) move(by time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock.Advance(by)
	close(s.moved)
	s.moved = make(chan struct{})
}

// MoveSources waits until every built-in event source is asleep, so each
// finished what it was doing, then moves the clock by by and wakes the
// sources whose sleep is over.
func (d *Daemon) MoveSources(by time.Duration) {
	d.t.Helper()
	d.sourcesAsleep()
	d.sources.move(by)
}

// sourcesAsleep waits until every built-in event source is asleep, so none
// is reading the files a test is about to change.
func (d *Daemon) sourcesAsleep() {
	d.t.Helper()
	deadline := time.Now().Add(Timeout)
	for {
		n := d.sources.asleep()
		if n > builtinSources {
			d.t.Fatalf("%d built-in event sources are asleep, the harness knows %d: raise builtinSources", n, builtinSources)
		}
		if n == builtinSources {
			break
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("only %d of %d built-in event sources fell asleep within %s", n, builtinSources, Timeout)
		}
		time.Sleep(time.Millisecond)
	}
}

// MountDisk adds the file system at path to the disk source's mount table,
// which it reads on its next sample.
func (d *Daemon) MountDisk(path string) {
	d.t.Helper()
	d.sourcesAsleep()
	name := filepath.Join(filepath.Dir(d.setup.cfgPath), mountsFile)
	table, err := os.ReadFile(name) //nolint:gosec // G304: the mount table in the test's own temporary folder
	if err != nil {
		d.t.Fatalf("read the mount table: %v", err)
	}
	table = fmt.Appendf(table, "/dev/test %s ext4 rw 0 0\n", path)
	if err := os.WriteFile(name, table, 0o600); err != nil { //nolint:gosec // G703: see above
		d.t.Fatalf("mount %s: %v", path, err)
	}
}
