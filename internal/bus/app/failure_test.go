// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/bus/app"
	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

var errBoom = errors.New("boom")

func appQuery() app.Query { return app.Query{} }

func contextWithCancel(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}

// failingAgents answers every lookup with errBoom.
type failingAgents struct{ core.AgentDirectory }

func (failingAgents) GetAgent(context.Context, string) (core.Agent, error) {
	return core.Agent{}, errBoom
}

// assertClosed fails unless ch closes, dropping what it still delivers.
func assertClosed(t *testing.T, ch <-chan domain.Message) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("stream did not close")
		}
	}
}

func TestFailures_AFailingAuditLogNeverFailsAnAction(t *testing.T) {
	f := newFixture(t)
	f.ports.Audit = failingAudit{f.ports.Audit}

	if _, err := f.svc.CreateChannel(asAgent("builder"), channel("general")); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if _, err := f.svc.Post(asAgent("builder"), toChannel("general", "hi")); err != nil {
		t.Fatalf("Post() error = %v", err)
	}
}

func TestFailures_AStoreFailureFailsTheSendAndShowsNobodyTheMessage(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))
	live, err := f.svc.Stream(asHuman("ada"), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.messages.appendErr = errBoom

	_, err = f.svc.Post(asAgent("builder"), toChannel("general", "hi"))

	if !errors.Is(err, errBoom) {
		t.Fatalf("Post() error = %v, want %v", err, errBoom)
	}
	select {
	case m := <-live:
		t.Fatalf("stream delivered %+v for a message that was never stored", m)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFailures_AReplyLookupFailureIsNotReportedAsABadReply(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))
	parent := post(asAgent("builder"), t, f, toChannel("general", "question"))
	f.messages.getErr = errBoom

	_, err := f.svc.Post(asAgent("intern"), domain.Draft{Channel: "general", Text: "answer", ReplyTo: parent.ID})

	if !errors.Is(err, errBoom) || errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Post() error = %v, want the storage error", err)
	}
}

func TestFailures_AFailingChannelTouchDoesNotFailTheSend(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))
	f.channels.touchErr = errBoom

	if _, err := f.svc.Post(asAgent("builder"), toChannel("general", "hi")); err != nil {
		t.Fatalf("Post() error = %v", err)
	}
}

func TestFailures_AChannelThatCannotBeReadHidesItsMessagesOnlyThisTime(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))
	post(asAgent("builder"), t, f, toChannel("general", "hi"))

	f.channels.getErr = errBoom
	broken, err := f.svc.ListMessages(asHuman("ada"), appQuery())
	if err != nil || len(broken.Messages) != 0 {
		t.Fatalf("while broken: %+v, %v", broken, err)
	}

	f.channels.getErr = nil
	healed, err := f.svc.ListMessages(asHuman("ada"), appQuery())
	if err != nil || len(healed.Messages) != 1 {
		t.Fatalf("after the repair: %+v, %v", healed, err)
	}
}

func TestFailures_AMessageOfAMissingChannelStaysHidden(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))
	post(asAgent("builder"), t, f, toChannel("general", "hi"))
	f.channels.getErr = core.ErrNotFound

	page, err := f.svc.ListMessages(asHuman("ada"), appQuery())

	if err != nil || len(page.Messages) != 0 {
		t.Fatalf("ListMessages() = %+v, %v", page, err)
	}
}

func TestFailures_ReadsReportStorageErrors(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))

	f.messages.beforeErr = errBoom
	if _, err := f.svc.ListMessages(asHuman("ada"), appQuery()); !errors.Is(err, errBoom) {
		t.Errorf("ListMessages() error = %v", err)
	}
	f.channels.listErr = errBoom
	if _, err := f.svc.ListChannels(asHuman("ada"), "", 0); !errors.Is(err, errBoom) {
		t.Errorf("ListChannels() error = %v", err)
	}
	f.messages.lastErr = errBoom
	if _, err := f.svc.Stream(asHuman("ada"), "", nil); !errors.Is(err, errBoom) {
		t.Errorf("Stream() error = %v", err)
	}
}

func TestFailures_AReplayThatCannotReadEndsTheStream(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))
	post(asAgent("builder"), t, f, toChannel("general", "hi"))
	f.messages.afterErr = errBoom
	after := uint64(0)

	ch, err := f.svc.Stream(asHuman("ada"), "", &after)
	if err != nil {
		t.Fatal(err)
	}

	assertClosed(t, ch)
}

func TestFailures_AReplayStopsWhenTheReaderLeaves(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))
	for i := range 40 {
		if i%30 == 0 {
			f.clock.Advance(window)
		}
		post(asAgent("builder"), t, f, toChannel("general", "x"))
	}
	ctx, cancel := contextWithCancel(asHuman("ada"))
	after := uint64(0)
	ch, err := f.svc.Stream(ctx, "", &after)
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	assertClosed(t, ch)
}

func TestFailures_AnAgentOutsideTheChartSeesPublicChannelsOnly(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))
	mustChannel(asAgent("builder"), t, f, teamChannel("team.founder"))

	page, err := f.svc.ListChannels(asAgent("ghost"), "", 1000)

	if err != nil || len(page.Channels) != 1 || page.Channels[0].Name != "general" {
		t.Fatalf("ListChannels() = %+v, %v", page, err)
	}
}

func TestFailures_ThePortReportsAFailingAgentDirectory(t *testing.T) {
	f := newFixture(t)
	f.ports.Agents = failingAgents{f.ports.Agents}

	_, err := f.svc.Send(asDaemon(), core.BusMessage{From: "agent:builder", To: "channel:general", Body: "x"})

	if !errors.Is(err, errBoom) {
		t.Fatalf("Send() error = %v, want %v", err, errBoom)
	}
}
