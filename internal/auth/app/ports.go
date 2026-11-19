// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"io"

	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// LicenceSource reads the engine licence shipped with the image.
type LicenceSource interface {
	// EngineLicence returns the engine name, licence version and text, or
	// core.ErrUnavailable when the image carries none.
	EngineLicence(ctx context.Context) (engine, version, text string, err error)
}

// Deps are what the identity use cases need.
type Deps struct {
	Clock    core.Clock
	Rand     io.Reader // crypto/rand.Reader in production
	Argon2   domain.Argon2Params
	Sessions domain.Sessions
	Tokens   domain.Tokens
	Humans   domain.Humans
	Setup    domain.Setup
	Licence  LicenceSource
	Events   core.EventPublisher
	Audit    core.AuditLog
	// Seed says whether the default society exists. ErrUnavailable, while
	// the agents module is not up, reads as not installed.
	Seed core.SeedStatus
}
