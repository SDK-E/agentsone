// SPDX-License-Identifier: MIT

package core

import (
	"crypto/rand"
	"fmt"
	"io"
	"regexp"
	"sync"

	"github.com/oklog/ulid/v2"
)

// IDs makes prefixed, sortable identifiers such as "agt_01JD3Q8Z5X6Y7W8V9T0S1R2Q3P".
// Modules use Deps.IDs instead of generating identifiers themselves, so tests
// get stable values.
type IDs interface {
	// New returns "<prefix>_<ULID>". The prefix must satisfy ValidPrefix;
	// an invalid prefix is a programming error and panics.
	New(prefix string) string
}

var prefixPattern = regexp.MustCompile(`^[a-z]{2,8}$`)

// ValidPrefix reports whether p is 2 to 8 lowercase ASCII letters.
func ValidPrefix(p string) bool { return prefixPattern.MatchString(p) }

// NewIDs makes IDs from clock time and entropy. Identifiers made within one
// millisecond still increase.
func NewIDs(clock Clock, entropy io.Reader) IDs {
	return &ulids{clock: clock, entropy: entropy}
}

// SystemIDs uses the system clock and crypto/rand.
func SystemIDs() IDs { return NewIDs(SystemClock{}, rand.Reader) }

type ulids struct {
	mu      sync.Mutex
	clock   Clock
	entropy io.Reader
	last    ulid.ULID
}

func (u *ulids) New(prefix string) string {
	if !ValidPrefix(prefix) {
		panic(fmt.Sprintf("core.IDs: invalid prefix %q, want 2 to 8 lowercase letters", prefix))
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.last = u.next()
	return prefix + "_" + u.last.String()
}

// next returns a fresh ULID, or the previous one plus one when the clock has
// not moved past it, so identifiers always increase.
func (u *ulids) next() ulid.ULID {
	ms := ulid.Timestamp(u.clock.Now())
	if ms <= u.last.Time() && u.last != (ulid.ULID{}) {
		return increment(u.last)
	}
	return ulid.MustNew(ms, u.entropy)
}

func increment(id ulid.ULID) ulid.ULID {
	for i := len(id) - 1; i >= 6; i-- {
		id[i]++
		if id[i] != 0 {
			return id
		}
	}
	panic("core.IDs: entropy exhausted within one millisecond")
}
