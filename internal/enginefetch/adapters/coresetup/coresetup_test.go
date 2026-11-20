// SPDX-License-Identifier: MIT

package coresetup_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/enginefetch/adapters/coresetup"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

type fakeSetup struct {
	mu       sync.Mutex
	accepted bool
	err      error
	saved    []core.EngineFetchStatus
}

func (f *fakeSetup) LicenceAccepted(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accepted, f.err
}

func (f *fakeSetup) SaveFetchStatus(_ context.Context, s core.EngineFetchStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, s)
	return nil
}

// newSetup logs as text without the time, so tests can compare whole lines.
func newSetup(p *core.Ports) (*coresetup.Setup, *bytes.Buffer) {
	var logs bytes.Buffer
	noTime := func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	}
	return coresetup.New(p, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{ReplaceAttr: noTime}))), &logs
}

func TestAcceptedAsksThePortOnEveryCall(t *testing.T) {
	p := core.NewPorts()
	s, _ := newSetup(p)
	fake := &fakeSetup{accepted: true}
	p.EngineSetup = fake // set after New: the port is read on each call
	got, err := s.Accepted(t.Context())
	if err != nil || !got {
		t.Fatalf("Accepted() = %v, %v, want true, nil", got, err)
	}
	fake.accepted = false
	if got, _ := s.Accepted(t.Context()); got {
		t.Error("Accepted() = true after the licence went away, want false")
	}
}

func TestAcceptedTreatsAnUnavailablePortAsNotAcceptedAndWarnsOnce(t *testing.T) {
	for name, p := range map[string]*core.Ports{
		"unavailable": core.NewPorts(),
		"nil":         {},
	} {
		t.Run(name, func(t *testing.T) {
			s, logs := newSetup(p)
			for range 3 {
				got, err := s.Accepted(t.Context())
				if err != nil || got {
					t.Fatalf("Accepted() = %v, %v, want false, nil", got, err)
				}
			}
			if n := strings.Count(logs.String(), "level=WARN"); n != 1 || strings.Count(logs.String(), "\n") != 1 {
				t.Errorf("want one warning about the missing port:\n%s", logs.String())
			}
			if !strings.Contains(logs.String(), core.ErrUnavailable.Error()) {
				t.Errorf("the warning must name the cause:\n%s", logs.String())
			}
		})
	}
}

// A new machine has no accepted licence yet: that is the normal first-boot
// state, so it is noted once at info and never warned about.
func TestAcceptedNotesAnUnacceptedLicenceOnceAtInfo(t *testing.T) {
	p := core.NewPorts()
	p.EngineSetup = &fakeSetup{accepted: false}
	s, logs := newSetup(p)
	for range 3 {
		got, err := s.Accepted(t.Context())
		if err != nil || got {
			t.Fatalf("Accepted() = %v, %v, want false, nil", got, err)
		}
	}
	want := `level=INFO msg="Claude engine not set up yet: a human must accept its licence"` + "\n"
	if logs.String() != want {
		t.Errorf("logs = %q, want %q", logs.String(), want)
	}
}

func TestAcceptedLogsNothingForAnAcceptedLicence(t *testing.T) {
	p := core.NewPorts()
	p.EngineSetup = &fakeSetup{accepted: true}
	s, logs := newSetup(p)
	if got, err := s.Accepted(t.Context()); err != nil || !got {
		t.Fatalf("Accepted() = %v, %v, want true, nil", got, err)
	}
	if logs.Len() != 0 {
		t.Errorf("logs = %q, want none", logs.String())
	}
}

func TestAcceptedPassesOtherErrorsOn(t *testing.T) {
	p := core.NewPorts()
	boom := errors.New("disk on fire")
	p.EngineSetup = &fakeSetup{err: boom}
	s, _ := newSetup(p)
	if _, err := s.Accepted(t.Context()); !errors.Is(err, boom) {
		t.Errorf("Accepted() error = %v, want %v", err, boom)
	}
}

func TestSaveStatusMapsEveryField(t *testing.T) {
	p := core.NewPorts()
	fake := &fakeSetup{}
	p.EngineSetup = fake
	s, _ := newSetup(p)
	at := time.Date(2026, 11, 20, 1, 2, 3, 0, time.UTC)
	err := s.SaveStatus(t.Context(), domain.Status{
		State: domain.StateFailed, BytesDone: 10, BytesTotal: 20, Version: "2.1.328", Error: "The download stopped.", UpdatedAt: at,
	})
	if err != nil {
		t.Fatalf("SaveStatus() error = %v", err)
	}
	want := []core.EngineFetchStatus{{State: "failed", BytesDone: 10, BytesTotal: 20, Version: "2.1.328", Error: "The download stopped.", UpdatedAt: at}}
	if diff := cmp.Diff(want, fake.saved); diff != "" {
		t.Errorf("saved (-want +got):\n%s", diff)
	}
}

func TestSaveStatusPassesThePortErrorOn(t *testing.T) {
	s, _ := newSetup(core.NewPorts())
	if err := s.SaveStatus(t.Context(), domain.Status{State: domain.StateQueued}); !errors.Is(err, core.ErrUnavailable) {
		t.Errorf("SaveStatus() error = %v, want ErrUnavailable", err)
	}
	s, _ = newSetup(&core.Ports{})
	if err := s.SaveStatus(t.Context(), domain.Status{State: domain.StateQueued}); !errors.Is(err, core.ErrUnavailable) {
		t.Errorf("SaveStatus() with a nil port error = %v, want ErrUnavailable", err)
	}
}
