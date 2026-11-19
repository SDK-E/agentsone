// SPDX-License-Identifier: MIT

package domain

import (
	"context"
	"time"
)

// Sessions stores sign-in sessions. Lookups of an unknown id answer
// core.ErrNotFound.
type Sessions interface {
	SaveSession(ctx context.Context, s Session) error
	Session(ctx context.Context, id string) (Session, error)
	DeleteSession(ctx context.Context, id string) error
	// DeleteHumanSessions ends every session of a human.
	DeleteHumanSessions(ctx context.Context, human string) error
}

// Tokens stores issued tokens. Lookups of an unknown id answer core.ErrNotFound.
type Tokens interface {
	SaveToken(ctx context.Context, t Token) error
	Token(ctx context.Context, id string) (Token, error)
	// RevokeToken sets RevokedAt when the token is live.
	RevokeToken(ctx context.Context, id string, at time.Time) error
}

// Humans stores human account details next to the Linux user. Lookups of
// an unknown name or uid answer core.ErrNotFound; a second human with the
// same name answers core.ErrConflict.
type Humans interface {
	AddHuman(ctx context.Context, h Human) error
	Human(ctx context.Context, name string) (Human, error)
	HumanByUID(ctx context.Context, uid int) (Human, error)
	ListHumans(ctx context.Context) ([]Human, error)
	UpdateHuman(ctx context.Context, h Human) error
	DeleteHuman(ctx context.Context, name string) error
}

// Licence is the recorded acceptance of the engine licence.
type Licence struct {
	Version    string
	AcceptedBy string
	AcceptedAt time.Time
}

// EngineFetch is where the engine download stands. The Claude runner work
// package writes it while fetching; this context only stores and reads it.
type EngineFetch struct {
	// State is a contract EngineFetchState value.
	State      string
	Progress   float64
	BytesDone  int64
	BytesTotal int64 // 0 when unknown
	Version    string
	Error      string
	UpdatedAt  time.Time
}

// Setup stores first-boot state. Reads before anything was stored answer
// the zero value and false.
type Setup interface {
	Licence(ctx context.Context) (Licence, bool, error)
	SaveLicence(ctx context.Context, l Licence) error
	EngineFetch(ctx context.Context) (EngineFetch, bool, error)
	SaveEngineFetch(ctx context.Context, f EngineFetch) error
}
