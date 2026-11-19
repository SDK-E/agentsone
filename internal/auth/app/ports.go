// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"io"

	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Authenticator checks a human's password: PAM in production. A wrong
// password or unknown user answers core.ErrUnauthorized.
type Authenticator interface {
	Authenticate(ctx context.Context, name, password string) error
}

// Accounts manages the Linux users behind human accounts, all in the group
// agentsone-humans.
type Accounts interface {
	// CreateUser adds the Linux user and returns its uid.
	CreateUser(ctx context.Context, name, password string) (int, error)
	SetPassword(ctx context.Context, name, password string) error
	DeleteUser(ctx context.Context, name string) error
}

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
	Sessions domain.Sessions
	Tokens   domain.Tokens
	Humans   domain.Humans
	Setup    domain.Setup
	Auth     Authenticator
	Accounts Accounts
	Licence  LicenceSource
	Events   core.EventPublisher
	Audit    core.AuditLog
}
