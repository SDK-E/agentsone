// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/freeze/domain"
)

var (
	t0 = time.Date(2026, 11, 19, 21, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Minute)
)

func TestState_FreezeTurnsTheSwitchOnAndNotesWhen(t *testing.T) {
	t.Parallel()
	got, change := domain.State{}.Freeze(t0)
	if !got.Frozen || !got.Since.Equal(t0) || change != domain.Froze {
		t.Fatalf("Freeze = %+v, %v; want frozen since %v, Froze", got, change, t0)
	}
}

func TestState_FreezeWhenFrozenChangesNothing(t *testing.T) {
	t.Parallel()
	on, _ := domain.State{}.Freeze(t0)
	got, change := on.Freeze(t1)
	if got != on || change != domain.Unchanged {
		t.Fatalf("second Freeze = %+v, %v; want %+v, Unchanged", got, change, on)
	}
}

func TestState_ReleaseTurnsTheSwitchOff(t *testing.T) {
	t.Parallel()
	on, _ := domain.State{}.Freeze(t0)
	got, change := on.Release(t1)
	if got.Frozen || !got.Since.Equal(t1) || change != domain.Released {
		t.Fatalf("Release = %+v, %v; want released since %v, Released", got, change, t1)
	}
}

func TestState_ReleaseWhenNotFrozenChangesNothing(t *testing.T) {
	t.Parallel()
	got, change := domain.State{}.Release(t1)
	if got != (domain.State{}) || change != domain.Unchanged {
		t.Fatalf("Release of open switch = %+v, %v; want zero, Unchanged", got, change)
	}
}
