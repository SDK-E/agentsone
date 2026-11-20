// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/bus/adapters/memory"
	"github.com/SDK-E/agentsone/internal/bus/app"
	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

// failingWaker answers err for every wake when err is set, else records it.
type failingWaker struct {
	*coretest.Waker
	err error
}

func (w *failingWaker) Wake(ctx context.Context, agent string, reason core.WakeReason, payload json.RawMessage) error {
	if w.err != nil {
		return w.err
	}
	return w.Waker.Wake(ctx, agent, reason, payload)
}

// flakyChannels fails the named calls with the error set for them.
type flakyChannels struct {
	*memory.Channels
	getErr, listErr, touchErr error
}

func (c *flakyChannels) Get(ctx context.Context, name string) (domain.Channel, error) {
	if c.getErr != nil {
		return domain.Channel{}, c.getErr
	}
	return c.Channels.Get(ctx, name)
}

func (c *flakyChannels) List(ctx context.Context, after string, limit int) ([]domain.Channel, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	return c.Channels.List(ctx, after, limit)
}

func (c *flakyChannels) Touch(ctx context.Context, name string, at time.Time) error {
	if c.touchErr != nil {
		return c.touchErr
	}
	return c.Channels.Touch(ctx, name, at)
}

// flakyMessages fails the named calls with the error set for them.
type flakyMessages struct {
	*memory.Messages
	appendErr, getErr, beforeErr, afterErr, lastErr error
}

func (m *flakyMessages) Append(ctx context.Context, msg domain.Message) (domain.Message, error) {
	if m.appendErr != nil {
		return domain.Message{}, m.appendErr
	}
	return m.Messages.Append(ctx, msg)
}

func (m *flakyMessages) Get(ctx context.Context, id string) (domain.Message, error) {
	if m.getErr != nil {
		return domain.Message{}, m.getErr
	}
	return m.Messages.Get(ctx, id)
}

func (m *flakyMessages) Before(ctx context.Context, f domain.MessageFilter, before uint64, limit int) ([]domain.Message, error) {
	if m.beforeErr != nil {
		return nil, m.beforeErr
	}
	return m.Messages.Before(ctx, f, before, limit)
}

func (m *flakyMessages) After(ctx context.Context, after uint64, limit int) ([]domain.Message, error) {
	if m.afterErr != nil {
		return nil, m.afterErr
	}
	return m.Messages.After(ctx, after, limit)
}

func (m *flakyMessages) Last(ctx context.Context) (uint64, error) {
	if m.lastErr != nil {
		return 0, m.lastErr
	}
	return m.Messages.Last(ctx)
}

// failingAudit refuses every entry.
type failingAudit struct{ core.AuditLog }

func (failingAudit) Append(context.Context, core.AuditEntry) error { return errors.New("audit down") }

type fixture struct {
	svc      *app.Service
	fakes    *coretest.Fakes
	ports    *core.Ports
	clock    *coretest.Clock
	waker    *failingWaker
	channels *flakyChannels
	messages *flakyMessages
}

// newFixture wires the service on in-memory repositories and the core fakes.
// The chart: founder > builder > intern, and outsider on its own.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	fakes.Waker.Strict(func(err error) { t.Error(err) })
	for _, a := range []core.Agent{
		{Name: "founder", Kind: core.AgentAlwaysOn},
		{Name: "builder", Kind: core.AgentChat, Manager: "founder"},
		{Name: "intern", Kind: core.AgentChat, Manager: "builder"},
		{Name: "outsider", Kind: core.AgentChat},
		{Name: "rooty", Kind: core.AgentChat, IsRoot: true},
	} {
		fakes.Agents.Add(a)
	}
	f := &fixture{
		fakes: fakes, ports: ports, clock: fakes.Clock, waker: &failingWaker{Waker: fakes.Waker},
		channels: &flakyChannels{Channels: memory.NewChannels()},
		messages: &flakyMessages{Messages: memory.NewMessages()},
	}
	ports.Waker = f.waker
	f.svc = app.New(app.Deps{
		Channels: f.channels, Messages: f.messages, Clock: fakes.Clock, IDs: fakes.IDs, Ports: ports,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return f
}

func (f *fixture) wakes() []coretest.Wake { return f.fakes.Waker.Wakes() }

func (f *fixture) auditEntries() []core.AuditEntry { return f.fakes.Audit.Entries() }

func asAgent(name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: name, UID: 1000})
}

func asRootAgent(name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerAgent, Name: name, IsRoot: true})
}

func asHuman(name string) context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerHuman, Name: name, UID: 1001})
}

func asPlugin() context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerPlugin, Name: "plg_01JD7ZK3M00000000000000001"})
}

// asDaemon is a context with no caller: the daemon's own work.
func asDaemon() context.Context { return context.Background() }

func channel(name string) domain.ChannelSpec {
	return domain.ChannelSpec{Name: name, Kind: domain.KindPublic, Description: "Test channel."}
}

func teamChannel(name string) domain.ChannelSpec {
	return domain.ChannelSpec{Name: name, Kind: domain.KindTeam}
}

func mustChannel(ctx context.Context, t *testing.T, f *fixture, spec domain.ChannelSpec) domain.Channel {
	t.Helper()
	c, err := f.svc.CreateChannel(ctx, spec)
	if err != nil {
		t.Fatalf("CreateChannel(%s) error = %v", spec.Name, err)
	}
	return c
}

func post(ctx context.Context, t *testing.T, f *fixture, d domain.Draft) domain.Message {
	t.Helper()
	m, err := f.svc.Post(ctx, d)
	if err != nil {
		t.Fatalf("Post(%+v) error = %v", d, err)
	}
	return m
}

func toChannel(name, text string) domain.Draft { return domain.Draft{Channel: name, Text: text} }

func toAgent(name, text string) domain.Draft { return domain.Draft{To: name, Text: text} }

func texts(ms []domain.Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Text)
	}
	return out
}

func recv(t *testing.T, ch <-chan domain.Message) domain.Message {
	t.Helper()
	select {
	case m, ok := <-ch:
		if !ok {
			t.Fatal("stream closed")
		}
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no message within 5s")
	}
	return domain.Message{}
}

func isStatus(err error, status int) bool {
	var problem *core.Problem
	return errors.As(err, &problem) && problem.Status == status
}

var window = time.Minute

var tooMany = http.StatusTooManyRequests
