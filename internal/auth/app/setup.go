// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/contract/eventdata"
	"github.com/SDK-E/agentsone/internal/core"
)

// Setup phases, with the contract's values. The Claude engine is off by
// default and turned on later in Settings, so first setup goes from
// needs-human straight to ready. The contract keeps needs-licence and
// fetching-engine for older clients; the daemon no longer enters them.
const (
	PhaseNeedsHuman = "needs-human"
	PhaseReady      = "ready"
)

// Engine fetch states, with the contract's values.
const (
	FetchWaitingLicence = "waiting-licence"
	FetchQueued         = "queued"
	FetchDownloading    = "downloading"
	FetchVerifying      = "verifying"
	FetchReady          = "ready"
	FetchFailed         = "failed"
)

var fetchStates = map[string]bool{
	FetchWaitingLicence: true, FetchQueued: true, FetchDownloading: true,
	FetchVerifying: true, FetchReady: true, FetchFailed: true,
}

// SetupStatus is where first boot stands.
type SetupStatus struct {
	Phase             string
	FirstHumanCreated bool
	LicenceAccepted   bool
	Engine            domain.EngineFetch
	// SeedInstalled is true once the Small Seed's founder, router and
	// waker exist. It does not change the phase.
	SeedInstalled bool
}

// LicenceView is the engine licence and whether it was accepted.
type LicenceView struct {
	Engine, Version, Text string
	Accepted              *domain.Licence
}

// SetupStatus reports first-boot progress.
func (s *Service) SetupStatus(ctx context.Context) (SetupStatus, error) {
	hs, err := s.d.Humans.ListHumans(ctx)
	if err != nil {
		return SetupStatus{}, fmt.Errorf("setup status: %w", err)
	}
	_, accepted, err := s.d.Setup.Licence(ctx)
	if err != nil {
		return SetupStatus{}, fmt.Errorf("setup status: %w", err)
	}
	fetch, err := s.EngineFetch(ctx)
	if err != nil {
		return SetupStatus{}, err
	}
	seeded, err := s.d.Seed.SeedInstalled(ctx)
	if err != nil && !errors.Is(err, core.ErrUnavailable) {
		return SetupStatus{}, fmt.Errorf("setup status: seed: %w", err)
	}
	st := SetupStatus{
		Phase: PhaseReady, FirstHumanCreated: len(hs) > 0, LicenceAccepted: accepted,
		Engine: fetch, SeedInstalled: seeded && err == nil,
	}
	if !st.FirstHumanCreated {
		st.Phase = PhaseNeedsHuman
	}
	return st, nil
}

// CreateFirstHuman creates the first account on first boot. Once any human
// exists it answers core.ErrConflict, so it cannot be used to add more.
func (s *Service) CreateFirstHuman(ctx context.Context, n NewHuman) (domain.Human, error) {
	s.humansMu.Lock()
	defer s.humansMu.Unlock()
	hs, err := s.d.Humans.ListHumans(ctx)
	if err != nil {
		return domain.Human{}, fmt.Errorf("first human: %w", err)
	}
	if len(hs) > 0 {
		return domain.Human{}, fmt.Errorf("a human already exists: %w", core.ErrConflict)
	}
	h, err := s.addHuman(ctx, n)
	if err != nil {
		return domain.Human{}, err
	}
	s.audit(ctx, core.Caller{Kind: core.CallerSystem, Name: "setup"}, "human.create-first", "human:"+h.Name)
	// The society seeds itself on this event; it is stored, so a daemon
	// restarted before seeding replays it.
	s.publish(ctx, eventdata.SetupFirstHumanCreated{Human: h.Name})
	return h, nil
}

// EngineLicence returns the licence text and its acceptance.
func (s *Service) EngineLicence(ctx context.Context) (LicenceView, error) {
	engine, version, text, err := s.d.Licence.EngineLicence(ctx)
	if err != nil {
		return LicenceView{}, fmt.Errorf("engine licence: %w", err)
	}
	v := LicenceView{Engine: engine, Version: version, Text: text}
	l, ok, err := s.d.Setup.Licence(ctx)
	if err != nil {
		return LicenceView{}, fmt.Errorf("engine licence: %w", err)
	}
	if ok && l.Version == version {
		v.Accepted = &l
	}
	return v, nil
}

// AcceptEngineLicence records that a human accepted the licence version
// shown to them and queues the engine fetch.
func (s *Service) AcceptEngineLicence(ctx context.Context, by core.Caller, version string, accept bool) (domain.EngineFetch, error) {
	if err := requireHuman(by); err != nil {
		return domain.EngineFetch{}, err
	}
	if !accept {
		return domain.EngineFetch{}, fmt.Errorf("accept must be true: %w", core.ErrInvalid)
	}
	engine, current, _, err := s.d.Licence.EngineLicence(ctx)
	if err != nil {
		return domain.EngineFetch{}, fmt.Errorf("accept licence: %w", err)
	}
	if version != current {
		return domain.EngineFetch{}, fmt.Errorf("licence version %q is not the current %q: %w", version, current, core.ErrConflict)
	}
	now := s.d.Clock.Now()
	if err := s.d.Setup.SaveLicence(ctx, domain.Licence{Version: version, AcceptedBy: by.Name, AcceptedAt: now}); err != nil {
		return domain.EngineFetch{}, fmt.Errorf("record licence: %w", err)
	}
	f := domain.EngineFetch{State: FetchQueued, UpdatedAt: now}
	if err := s.d.Setup.SaveEngineFetch(ctx, f); err != nil {
		return domain.EngineFetch{}, fmt.Errorf("queue engine fetch: %w", err)
	}
	s.audit(ctx, by, "setup.licence-accept", "licence:"+version)
	s.publish(ctx, eventdata.SetupLicenceAccepted{Engine: engine, Version: version, AcceptedBy: by.Name})
	return f, nil
}

// EngineFetch returns the engine download status.
func (s *Service) EngineFetch(ctx context.Context) (domain.EngineFetch, error) {
	f, ok, err := s.d.Setup.EngineFetch(ctx)
	if err != nil {
		return domain.EngineFetch{}, fmt.Errorf("engine fetch status: %w", err)
	}
	if !ok {
		return domain.EngineFetch{State: FetchWaitingLicence, UpdatedAt: s.d.Clock.Now()}, nil
	}
	return f, nil
}
