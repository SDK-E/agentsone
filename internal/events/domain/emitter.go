// SPDX-License-Identifier: MIT

package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// EmitterKind says where an emitter runs.
type EmitterKind string

// Emitter kinds, with the contract's values.
const (
	KindBuiltin EmitterKind = "builtin"
	KindProcess EmitterKind = "process"
	KindWebhook EmitterKind = "webhook"
)

const (
	maxDescription = 500
	maxEventTypes  = 50
	maxCommandArgs = 64
	maxArgLen      = 4096
	// WebhookPrefix is where webhook intake paths start.
	WebhookPrefix = "/v1/webhooks/"
	// SignaturePrefix starts the X-Agentsone-Signature value.
	SignaturePrefix = "sha256="
)

var emitterNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// Emitter is a source of events: built into the daemon, a process run by
// systemd as emitter@<id>.service, or a signed webhook.
type Emitter struct {
	ID          string
	Name        string
	Kind        EmitterKind
	Description string
	EventTypes  []string // patterns, see MatchPattern
	Enabled     bool
	Unit        string // "emitter@<id>.service" for process emitters, else empty
	Owner       string // "<kind>:<name>"
	PluginID    string
	WebhookPath string
	CreatedAt   time.Time
	LastEventAt *time.Time
	// Command is the program and arguments of a process emitter.
	Command []string
	// Secret is the HMAC key of a webhook emitter. It is shown once and never listed.
	Secret string
}

// Registration is a request to add a process or webhook emitter.
type Registration struct {
	Name        string
	Kind        EmitterKind
	Description string
	EventTypes  []string
	Command     []string
}

// Validate checks the registration rules. Event types must live under
// agentsone.<name>. so no emitter can pose as a built-in one.
func (r Registration) Validate() error {
	if r.Kind != KindProcess && r.Kind != KindWebhook {
		return fmt.Errorf("%w: kind must be process or webhook", ErrInvalidEmitter)
	}
	if !emitterNamePattern.MatchString(r.Name) {
		return fmt.Errorf("%w: name must be lowercase letters, digits and hyphens", ErrInvalidEmitter)
	}
	if len(r.Description) > maxDescription {
		return fmt.Errorf("%w: description is over %d characters", ErrInvalidEmitter, maxDescription)
	}
	if err := r.validateTypes(); err != nil {
		return err
	}
	return r.validateCommand()
}

func (r Registration) validateTypes() error {
	if len(r.EventTypes) == 0 || len(r.EventTypes) > maxEventTypes {
		return fmt.Errorf("%w: give 1 to %d event types", ErrInvalidEmitter, maxEventTypes)
	}
	allowed := regexp.MustCompile(`^agentsone\.` + regexp.QuoteMeta(r.Name) + `\.([a-z][a-z0-9-]*\.)*([a-z][a-z0-9-]*|\*)$`)
	for _, t := range r.EventTypes {
		if len(t) > 200 || !allowed.MatchString(t) {
			return fmt.Errorf("%w: event type %q must be agentsone.%s.<event> or end in .*", ErrInvalidEmitter, t, r.Name)
		}
	}
	return nil
}

func (r Registration) validateCommand() error {
	if r.Kind == KindWebhook {
		if len(r.Command) > 0 {
			return fmt.Errorf("%w: a webhook emitter has no command", ErrInvalidEmitter)
		}
		return nil
	}
	if len(r.Command) == 0 || len(r.Command) > maxCommandArgs || !strings.HasPrefix(r.Command[0], "/") {
		return fmt.Errorf("%w: command must start with an absolute program path", ErrInvalidEmitter)
	}
	for _, arg := range r.Command {
		if arg == "" || len(arg) > maxArgLen || strings.ContainsAny(arg, "\x00\n\r") {
			return fmt.Errorf("%w: command arguments must be non-empty single lines", ErrInvalidEmitter)
		}
	}
	return nil
}

// NewEmitter builds the emitter for a valid registration.
func NewEmitter(r Registration, id, owner string, now time.Time) Emitter {
	e := Emitter{
		ID: id, Name: r.Name, Kind: r.Kind, Description: r.Description,
		EventTypes: append([]string(nil), r.EventTypes...), Enabled: true, Owner: owner, CreatedAt: now,
	}
	switch r.Kind {
	case KindProcess:
		e.Unit = "emitter@" + id + ".service"
		e.Command = append([]string(nil), r.Command...)
	case KindWebhook:
		e.WebhookPath = WebhookPrefix + id
	case KindBuiltin:
	}
	return e
}

// Allows reports whether the emitter may publish an event of type t.
func (e Emitter) Allows(t string) bool {
	for _, p := range e.EventTypes {
		if MatchPattern(p, t) {
			return true
		}
	}
	return false
}

// MayPublish reports whether c may publish events as this emitter. The
// system and the host may publish for any emitter, plugins for built-in ones,
// and anyone else only for an emitter they own.
func (e Emitter) MayPublish(c core.Caller) bool {
	switch c.Kind {
	case core.CallerSystem, core.CallerHost:
		return true
	case core.CallerPlugin:
		return e.Kind == KindBuiltin || e.Owner == "plugin:"+c.Name
	case core.CallerAgent, core.CallerHuman:
		return e.Owner == string(c.Kind)+":"+c.Name
	}
	return false
}

// BuiltinEmitters returns the nine emitters every machine starts with. The
// programs behind most of them are separate; these are their records.
func BuiltinEmitters() []Emitter {
	specs := []struct{ name, text string }{
		{"auth", "Sign-ins, failed sign-ins and sign-outs."},
		{"services", "systemd units that start, stop or fail."},
		{"disk", "File systems that fill up or recover."},
		{"load", "CPU and memory pressure."},
		{"files", "Files created, changed or removed in watched folders."},
		{"git", "Commits and branch changes in watched repositories."},
		{"agents", "Agents hired, edited, retired, woken, asleep or failing."},
		{"clock", "Time ticks and fired schedules."},
		{"webhooks", "HTTP requests that arrived at a webhook endpoint."},
	}
	out := make([]Emitter, 0, len(specs))
	for _, s := range specs {
		out = append(out, Emitter{
			Name: s.name, Kind: KindBuiltin, Description: s.text, Enabled: true,
			EventTypes: []string{"agentsone." + s.name + ".*"}, Owner: "system:daemon",
		})
	}
	return out
}

// BuiltinEmitterType reports whether an event type belongs to a built-in
// emitter, that is whether its second segment is the name of one.
func BuiltinEmitterType(eventType string) bool {
	parts := strings.Split(eventType, ".")
	if len(parts) < 3 || parts[0] != "agentsone" {
		return false
	}
	for _, e := range BuiltinEmitters() {
		if e.Name == parts[1] {
			return true
		}
	}
	return false
}

// Sign returns the X-Agentsone-Signature value for body under secret.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return SignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature checks a signature header in constant time.
func VerifySignature(secret string, body []byte, header string) bool {
	hexSum, ok := strings.CutPrefix(header, SignaturePrefix)
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSum)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
