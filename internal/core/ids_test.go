// SPDX-License-Identifier: MIT

package core_test

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

type stoppedClock struct{ t time.Time }

func (c stoppedClock) Now() time.Time { return c.t }

var idPattern = regexp.MustCompile(`^agt_[0-9A-HJKMNP-TV-Z]{26}$`)

func TestIDsFormat(t *testing.T) {
	ids := core.SystemIDs()
	id := ids.New("agt")
	if !idPattern.MatchString(id) {
		t.Fatalf("id %q does not match prefix_ULID", id)
	}
}

func TestIDsMonotonicWithinOneMillisecond(t *testing.T) {
	ids := core.NewIDs(stoppedClock{time.Unix(1_700_000_000, 0)}, bytes.NewReader(make([]byte, 1024)))
	prev := ""
	for range 50 {
		id := ids.New("tsk")
		if id <= prev {
			t.Fatalf("id %q not after %q", id, prev)
		}
		prev = id
	}
}

func TestIDsDeterministicForSameInputs(t *testing.T) {
	newIDs := func() core.IDs {
		return core.NewIDs(stoppedClock{time.Unix(1_700_000_000, 0)}, bytes.NewReader(make([]byte, 64)))
	}
	a, b := newIDs(), newIDs()
	if x, y := a.New("evt"), b.New("evt"); x != y {
		t.Fatalf("same inputs gave %q and %q", x, y)
	}
}

func TestIDsSafeForConcurrentUse(t *testing.T) {
	ids := core.SystemIDs()
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[string]bool{}
	for range 8 {
		wg.Go(func() {
			for range 100 {
				id := ids.New("evt")
				mu.Lock()
				seen[id] = true
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(seen) != 800 {
		t.Fatalf("got %d unique ids, want 800", len(seen))
	}
}

func TestValidPrefix(t *testing.T) {
	for _, p := range []string{"ab", "agt", "abcdefgh"} {
		if !core.ValidPrefix(p) {
			t.Errorf("%q rejected", p)
		}
	}
	for _, p := range []string{"", "a", "abcdefghi", "Agt", "a1", "a_b"} {
		if core.ValidPrefix(p) {
			t.Errorf("%q accepted", p)
		}
	}
}

func TestIDsPanicOnInvalidPrefix(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(r.(string), "Bad") {
			t.Fatalf("recovered %v", r)
		}
	}()
	core.SystemIDs().New("Bad")
}
