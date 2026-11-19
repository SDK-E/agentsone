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

func newSetup(p *core.Ports) (*coresetup.Setup, *bytes.Buffer) {
	var logs bytes.Buffer
	return coresetup.New(p, slog.New(slog.NewTextHandler(&logs, nil))), &logs
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

func TestAcceptedTreatsAnUnavailablePortAsNotAcceptedAndLogsOnce(t *testing.T) {
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
			if n := strings.Count(logs.String(), "engine setup"); n != 1 {
				t.Errorf("log lines about the missing port = %d, want 1:\n%s", n, logs.String())
			}
		})
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
