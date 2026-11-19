// SPDX-License-Identifier: MIT

package app_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func TestPost_ToAChannelStoresTheMessageAndWakesNobody(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))
	f.clock.Advance(window)

	m, err := f.svc.Post(asAgent("intern"), toChannel("general", "Standup in five."))

	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if !strings.HasPrefix(m.ID, "msg_") || m.Channel != "general" || m.To != "" || m.From != "agent:intern" ||
		m.Text != "Standup in five." || m.ReplyTo != "" || m.CorrelationID != m.ID || !m.CreatedAt.Equal(f.clock.Now()) {
		t.Fatalf("message = %+v", m)
	}
	got, _ := f.svc.GetChannel(asHuman("ada"), "general")
	if got.LastMessageAt == nil || !got.LastMessageAt.Equal(f.clock.Now()) {
		t.Fatalf("channel LastMessageAt = %v, want %v", got.LastMessageAt, f.clock.Now())
	}
	if len(f.wakes()) != 0 {
		t.Fatalf("a channel message woke %+v", f.wakes())
	}
}

func TestPost_TheSenderIsAlwaysTheCaller(t *testing.T) {
	f := newFixture(t)
	mustChannel(asHuman("ada"), t, f, channel("general"))

	byHuman := post(asHuman("ada"), t, f, toChannel("general", "hello"))
	byDaemon := post(asDaemon(), t, f, toChannel("general", "hello"))

	if byHuman.From != "human:ada" || byDaemon.From != "system:daemon" {
		t.Fatalf("senders = %q, %q", byHuman.From, byDaemon.From)
	}
}

func TestPost_TeamChannelsNeedMembership(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, teamChannel("team.founder"))

	if _, err := f.svc.Post(asAgent("intern"), toChannel("team.founder", "member")); err != nil {
		t.Fatalf("member post error = %v", err)
	}
	if _, err := f.svc.Post(asHuman("ada"), toChannel("team.founder", "human")); err != nil {
		t.Fatalf("human post error = %v", err)
	}
	if _, err := f.svc.Post(asAgent("outsider"), toChannel("team.founder", "stranger")); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("stranger post error = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.Post(asAgent("intern"), toChannel("ghost", "x")); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown channel error = %v, want ErrNotFound", err)
	}
}

func TestPost_DirectMessageWakesTheRecipientWithReasonMessage(t *testing.T) {
	f := newFixture(t)

	m := post(asAgent("builder"), t, f, toAgent("intern", "Please review PR 7."))

	wakes := f.wakes()
	if len(wakes) != 1 || wakes[0].Agent != "intern" || wakes[0].Reason != core.WakeMessage {
		t.Fatalf("wakes = %+v", wakes)
	}
	var payload map[string]any
	if err := json.Unmarshal(wakes[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["message_id"] != m.ID || payload["from"] != "agent:builder" || payload["text"] != "Please review PR 7." {
		t.Fatalf("payload = %v", payload)
	}
	if m.To != "intern" || m.Channel != "" {
		t.Fatalf("message = %+v", m)
	}
}

func TestPost_DirectMessageRefusals(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Post(asAgent("builder"), toAgent("ghost", "x")); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown recipient error = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.Post(asAgent("builder"), toAgent("builder", "x")); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("message to self error = %v, want ErrInvalid", err)
	}
	if len(f.wakes()) != 0 {
		t.Fatalf("a refused message woke %+v", f.wakes())
	}
	if got, _ := f.messages.Last(t.Context()); got != 0 {
		t.Fatalf("a refused message was stored (last sequence %d)", got)
	}
}

func TestPost_AFrozenRecipientGetsTheMessageButIsNotWoken(t *testing.T) {
	f := newFixture(t)
	f.fakes.Waker.Freeze("intern")

	m, err := f.svc.Post(asAgent("builder"), toAgent("intern", "Wake up."))

	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if len(f.wakes()) != 0 {
		t.Fatalf("a frozen agent was woken: %+v", f.wakes())
	}
	if _, err := f.messages.Get(t.Context(), m.ID); err != nil {
		t.Fatalf("the message was not stored: %v", err)
	}
	if !woke(t, f, m.ID, false) {
		t.Fatal("audit does not say that the wake was skipped")
	}
}

func TestPost_AFailingWakeDoesNotFailTheSend(t *testing.T) {
	f := newFixture(t)
	f.waker.err = errors.New("boom")

	m, err := f.svc.Post(asAgent("builder"), toAgent("intern", "Hello."))

	if err != nil || m.ID == "" {
		t.Fatalf("Post() = %+v, %v", m, err)
	}
	if !woke(t, f, m.ID, false) {
		t.Fatal("audit does not say that the wake failed")
	}
}

// woke reports whether the audit entry of message id carries woke == want.
func woke(t *testing.T, f *fixture, id string, want bool) bool {
	t.Helper()
	for _, e := range f.auditEntries() {
		if e.Action != "bus.send" || e.Target != "message:"+id {
			continue
		}
		var detail map[string]any
		if err := json.Unmarshal(e.Detail, &detail); err != nil {
			t.Fatal(err)
		}
		return detail["woke"] == want
	}
	t.Fatalf("no bus.send audit entry for %s", id)
	return false
}

func TestPost_AuditsEverySendWithoutTheText(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))

	post(asAgent("builder"), t, f, toChannel("general", "secret plans"))
	m := post(asAgent("builder"), t, f, toAgent("intern", "more secret plans"))

	var sends []string
	for _, e := range f.auditEntries() {
		if e.Action == "bus.send" {
			sends = append(sends, e.Target)
			if e.Actor != "agent:builder" || strings.Contains(string(e.Detail), "secret") {
				t.Fatalf("audit entry = %+v", e)
			}
		}
	}
	if len(sends) != 2 || sends[1] != "message:"+m.ID {
		t.Fatalf("send targets = %v", sends)
	}
}

func TestPost_RepliesKeepTheConversationTogether(t *testing.T) {
	f := newFixture(t)
	first := post(asAgent("builder"), t, f, toAgent("intern", "Question?"))
	reply := domain.Draft{To: "builder", Text: "Answer.", ReplyTo: first.ID}

	second := post(asAgent("intern"), t, f, reply)
	third := post(asAgent("builder"), t, f, domain.Draft{To: "intern", Text: "Thanks.", ReplyTo: second.ID})

	if second.ReplyTo != first.ID || second.CorrelationID != first.ID || third.CorrelationID != first.ID {
		t.Fatalf("correlation = %q, %q; want %q", second.CorrelationID, third.CorrelationID, first.ID)
	}
}

func TestPost_ReplyToAMessageTheSenderCannotSeeIsInvalid(t *testing.T) {
	f := newFixture(t)
	private := post(asAgent("builder"), t, f, toAgent("intern", "Private."))
	mustChannel(asAgent("builder"), t, f, teamChannel("team.founder"))
	team := post(asAgent("builder"), t, f, toChannel("team.founder", "Team only."))
	mustChannel(asAgent("outsider"), t, f, channel("open"))
	tests := []struct {
		name    string
		replyTo string
	}{
		{"direct message of others", private.ID},
		{"team channel message of another branch", team.ID},
		{"unknown message", "msg_01JD7ZK3M00000000000000009"},
	}
	for _, tt := range tests {
		d := domain.Draft{Channel: "open", Text: "Peeking.", ReplyTo: tt.replyTo}
		if _, err := f.svc.Post(asAgent("outsider"), d); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", tt.name, err)
		}
	}
}

func TestPost_RefusesWhatTheDraftForbids(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name  string
		draft domain.Draft
	}{
		{"neither channel nor recipient", domain.Draft{Text: "x"}},
		{"both channel and recipient", domain.Draft{Channel: "general", To: "intern", Text: "x"}},
		{"empty text", domain.Draft{To: "intern", Text: ""}},
		{"text over the limit", domain.Draft{To: "intern", Text: strings.Repeat("x", domain.MaxText+1)}},
	}
	for _, tt := range tests {
		if _, err := f.svc.Post(asAgent("builder"), tt.draft); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", tt.name, err)
		}
	}
}

func TestPost_PluginsCannotSend(t *testing.T) {
	f := newFixture(t)
	mustChannel(asHuman("ada"), t, f, channel("general"))

	if _, err := f.svc.Post(asPlugin(), toChannel("general", "x")); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("plugin post error = %v, want ErrForbidden", err)
	}
}

// TESTING: a runaway sender is stopped. 120 sends per minute are allowed, the
// 121st is refused, and sending works again once the window has passed.
func TestPost_RateLimitsEachSender(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))
	ctx := asAgent("builder")
	for i := range 119 { // the channel creation is not a send
		if _, err := f.svc.Post(ctx, toChannel("general", "x")); err != nil {
			t.Fatalf("send %d error = %v", i, err)
		}
	}
	f.clock.Advance(window / 2)
	if _, err := f.svc.Post(ctx, toChannel("general", "x")); err != nil {
		t.Fatalf("send 120 error = %v", err)
	}

	_, err := f.svc.Post(ctx, toChannel("general", "x"))

	if !isStatus(err, tooMany) {
		t.Fatalf("send 121 error = %v, want 429", err)
	}
	if _, err := f.svc.Post(asAgent("intern"), toChannel("general", "x")); err != nil {
		t.Fatalf("another sender was limited: %v", err)
	}
	f.clock.Advance(window)
	if _, err := f.svc.Post(ctx, toChannel("general", "x")); err != nil {
		t.Fatalf("send after the window error = %v", err)
	}
}

func TestPost_AnInvalidDraftDoesNotUseUpTheRate(t *testing.T) {
	f := newFixture(t)
	for range 200 {
		_, _ = f.svc.Post(asAgent("builder"), domain.Draft{Text: "no target"})
	}

	if _, err := f.svc.Post(asAgent("builder"), toAgent("intern", "ok")); err != nil {
		t.Fatalf("send after invalid drafts error = %v", err)
	}
}

func TestSend_Port(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))

	toChan, err := f.svc.Send(asDaemon(), core.BusMessage{From: "agent:builder", To: "channel:general", Body: "From the runner."})
	if err != nil {
		t.Fatalf("Send(channel) error = %v", err)
	}
	if toChan.From != "agent:builder" || toChan.To != "channel:general" || toChan.Body != "From the runner." ||
		!strings.HasPrefix(toChan.ID, "msg_") || !toChan.Sent.Equal(f.clock.Now()) {
		t.Fatalf("Send(channel) = %+v", toChan)
	}
	direct, err := f.svc.Send(asDaemon(), core.BusMessage{From: "agent:builder", To: "agent:intern", Body: "Wake up."})
	if err != nil || direct.To != "agent:intern" {
		t.Fatalf("Send(agent) = %+v, %v", direct, err)
	}
	if wakes := f.wakes(); len(wakes) != 1 || wakes[0].Agent != "intern" || wakes[0].Reason != core.WakeMessage {
		t.Fatalf("wakes = %+v", wakes)
	}
	stored, err := f.messages.Get(t.Context(), direct.ID)
	if err != nil || stored.From != "agent:builder" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

func TestSend_PortRefusals(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name    string
		ctx     func() error
		wantErr error
	}{
		{"empty body", func() error {
			_, err := f.svc.Send(asDaemon(), core.BusMessage{From: "agent:builder", To: "agent:intern"})
			return err
		}, core.ErrInvalid},
		{"target without a kind", func() error {
			_, err := f.svc.Send(asDaemon(), core.BusMessage{From: "agent:builder", To: "intern", Body: "x"})
			return err
		}, core.ErrInvalid},
		{"sender is not an actor", func() error {
			_, err := f.svc.Send(asDaemon(), core.BusMessage{From: "builder", To: "agent:intern", Body: "x"})
			return err
		}, core.ErrInvalid},
		{"sender is not in the chart", func() error {
			_, err := f.svc.Send(asDaemon(), core.BusMessage{From: "agent:ghost", To: "agent:intern", Body: "x"})
			return err
		}, core.ErrInvalid},
		{"plugin sender", func() error {
			_, err := f.svc.Send(asDaemon(), core.BusMessage{From: "plugin:plg_01JD7ZK3M00000000000000001", To: "agent:intern", Body: "x"})
			return err
		}, core.ErrForbidden},
		{"an agent speaking as another agent", func() error {
			_, err := f.svc.Send(asAgent("intern"), core.BusMessage{From: "agent:builder", To: "agent:founder", Body: "x"})
			return err
		}, core.ErrForbidden},
	}
	for _, tt := range tests {
		if err := tt.ctx(); !errors.Is(err, tt.wantErr) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.wantErr)
		}
	}
	if len(f.wakes()) != 0 {
		t.Fatalf("a refused message woke %+v", f.wakes())
	}
}

func TestSend_PortTrustsTheDaemonButNotAnAgentCaller(t *testing.T) {
	f := newFixture(t)

	own, err := f.svc.Send(asAgent("builder"), core.BusMessage{From: "agent:builder", To: "agent:intern", Body: "mine"})
	if err != nil || own.From != "agent:builder" {
		t.Fatalf("own message = %+v, %v", own, err)
	}
	root, err := f.svc.Send(asRootAgent("rooty"), core.BusMessage{From: "agent:founder", To: "agent:intern", Body: "as founder"})
	if err != nil || root.From != "agent:founder" {
		t.Fatalf("root message = %+v, %v", root, err)
	}
}
