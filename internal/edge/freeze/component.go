// SPDX-License-Identifier: MIT

// Package freeze is the freeze-all switch of the edge, a hard wall: while it is on, the internet
// filter refuses all egress of the machine and the model gateway refuses every model call with
// problem urn:agentsone:problem:frozen. Only the control page turns it on or off, through the
// admin socket (GET, POST and DELETE /v1/freeze). It survives an edge restart, and every change
// is recorded as edge proof in the edge audit log.
//
// Per-agent and per-team freezes are soft and live in the daemon, not here.
//
// Shape: domain (the switch as a value), app (keeps egress, the gate and the saved state in step),
// adapters/file (state file), adapters/admin (routes and the model-call gate). This file wires
// them as an edge component.
//
// Owned by work package 16 (edge: gateway, budget, audit sink, web door, freeze all for model calls).
package freeze

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/component"
	"github.com/SDK-E/agentsone/internal/edge/freeze/adapters/admin"
	"github.com/SDK-E/agentsone/internal/edge/freeze/adapters/file"
	"github.com/SDK-E/agentsone/internal/edge/freeze/app"
)

// Component is the freeze-all switch. It starts after the internet filter: it takes the filter's
// Freezer from env and puts the whole switch in its place, so the parts that start later (the
// gateway, the door) see freeze all, not just the egress cut.
type Component struct{}

var _ component.Component = Component{}

// Name names the component.
func (Component) Name() string { return "freeze" }

// ErrNoFilter is returned by Start when the internet filter has not shared its Freezer.
var ErrNoFilter = errors.New("freeze: the internet filter must start first and share its freezer")

// Start loads the switch, holds a saved freeze and adds the admin routes.
func (Component) Start(ctx context.Context, env *component.Env) (component.Stopper, error) {
	if env.Freezer == nil {
		return nil, ErrNoFilter
	}
	var proofs app.Proofs
	if env.Proof != nil {
		proofs = proofRecorder{env.Proof}
	}
	svc, err := app.Open(ctx, app.Deps{
		Egress: env.Freezer,
		Store:  file.New(filepath.Join(env.DataDir, file.FileName)),
		Proofs: proofs,
		Clock:  systemClock{},
		Log:    env.Log,
	})
	if err != nil {
		return nil, err
	}
	admin.Register(env.Admin, svc, env.Log)
	env.Freezer = svc
	return noStop{}, nil
}

// GateModelCalls wraps the model gateway's handler: while freeze all is on, every model call is
// refused with 423 and problem type urn:agentsone:problem:frozen, without Retry-After. Pass
// env.Freezer as seen after this component started.
func GateModelCalls(f component.Freezer, next http.Handler) http.Handler {
	return admin.GateModelCalls(f, next)
}

// Target is what freeze proof records are about.
const Target = "machine:all"

type proofRecorder struct{ r component.ProofRecorder }

func (p proofRecorder) RecordProof(ctx context.Context, pr app.Proof) error {
	return p.r.RecordProof(ctx, component.Proof{
		Action: pr.Action, Target: Target, Outcome: pr.Outcome, Details: pr.Details,
	})
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

type noStop struct{}

func (noStop) Stop() error { return nil }
