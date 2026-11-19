// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

const problemBase = core.ProblemTypeBase

func createChannel(t *testing.T, e *env, c core.Caller, name, kind string) api.Channel {
	t.Helper()
	rec := e.do(t, c, http.MethodPost, "/v1/bus/channels", obj{"name": name, "kind": kind, "description": "About " + name + "."})
	return decode[api.Channel](t, rec, http.StatusCreated)
}

func send(t *testing.T, e *env, c core.Caller, body obj) api.BusMessage {
	t.Helper()
	return decode[api.BusMessage](t, e.do(t, c, http.MethodPost, "/v1/bus/messages", body), http.StatusCreated)
}

func TestCreateChannel_ReturnsThePublicChannel(t *testing.T) {
	e := newEnv(t)

	got := createChannel(t, e, builder, "general", "public")

	if got.Name != "general" || got.Kind != api.ChannelKindPublic || got.Team != nil || got.LastMessageAt != nil ||
		got.CreatedBy != "agent:builder" || got.Description != "About general." {
		t.Fatalf("channel = %+v", got)
	}
}

func TestCreateChannel_TeamChannelNamesTheCreatorsTeam(t *testing.T) {
	e := newEnv(t)

	got := createChannel(t, e, intern, "team.founder", "team")

	if got.Kind != api.ChannelKindTeam || got.Team == nil || *got.Team != "founder" {
		t.Fatalf("channel = %+v", got)
	}
}

func TestCreateChannel_Refusals(t *testing.T) {
	e := newEnv(t)
	createChannel(t, e, builder, "general", "public")
	tests := []struct {
		name   string
		caller core.Caller
		body   obj
		status int
		typ    string
	}{
		{"name taken", builder, obj{"name": "general", "kind": "public"}, http.StatusConflict, "conflict"},
		{"bad name", builder, obj{"name": "Not Valid", "kind": "public"}, http.StatusBadRequest, "validation"},
		{"team channel by a human", human, obj{"name": "team.x", "kind": "team"}, http.StatusBadRequest, "validation"},
	}
	for _, tt := range tests {
		rec := e.do(t, tt.caller, http.MethodPost, "/v1/bus/channels", tt.body)
		if got := problemType(t, rec, tt.status); got != problemBase+tt.typ {
			t.Errorf("%s: problem type = %q", tt.name, got)
		}
	}
}

func TestGetChannel_AnswersNotFoundForTheTeamChannelOfAnotherBranch(t *testing.T) {
	e := newEnv(t)
	createChannel(t, e, builder, "team.founder", "team")

	own := e.do(t, intern, http.MethodGet, "/v1/bus/channels/team.founder", nil)
	other := e.do(t, outsider, http.MethodGet, "/v1/bus/channels/team.founder", nil)
	missing := e.do(t, outsider, http.MethodGet, "/v1/bus/channels/nothing", nil)

	if got := decode[api.Channel](t, own, http.StatusOK); got.Name != "team.founder" {
		t.Fatalf("own = %+v", got)
	}
	// Another branch's channel and a channel that does not exist answer alike.
	if got := problemType(t, other, http.StatusNotFound); got != problemBase+"not-found" {
		t.Fatalf("other branch: problem type = %q", got)
	}
	if got := problemType(t, missing, http.StatusNotFound); got != problemBase+"not-found" {
		t.Fatalf("missing: problem type = %q", got)
	}
}

func TestListChannels_PagesByName(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		createChannel(t, e, builder, name, "public")
	}

	first := decode[api.ChannelList](t, e.do(t, human, http.MethodGet, "/v1/bus/channels?limit=2", nil), http.StatusOK)
	if len(first.Items) != 2 || first.NextCursor == nil || first.Items[0].Name != "alpha" || first.Items[1].Name != "beta" {
		t.Fatalf("first page = %+v", first)
	}
	second := decode[api.ChannelList](t, e.do(t, human, http.MethodGet, "/v1/bus/channels?limit=2&cursor="+*first.NextCursor, nil), http.StatusOK)
	if len(second.Items) != 1 || second.Items[0].Name != "gamma" || second.NextCursor != nil {
		t.Fatalf("second page = %+v", second)
	}
}

func TestSendBusMessage_ToAChannel(t *testing.T) {
	e := newEnv(t)
	createChannel(t, e, builder, "general", "public")

	got := send(t, e, builder, obj{"channel": "general", "text": "Hello."})

	if got.Channel == nil || *got.Channel != "general" || got.To != nil || got.From != "agent:builder" || got.Text != "Hello." ||
		!strings.HasPrefix(got.Id, "msg_") || got.CorrelationId == nil || *got.CorrelationId != got.Id {
		t.Fatalf("message = %+v", got)
	}
	if len(e.fakes.Waker.Wakes()) != 0 {
		t.Fatal("a channel message woke someone")
	}
}

func TestSendBusMessage_DirectMessageWakesTheRecipient(t *testing.T) {
	e := newEnv(t)

	got := send(t, e, builder, obj{"to": "intern", "text": "Wake up."})

	if got.To == nil || *got.To != "intern" || got.Channel != nil {
		t.Fatalf("message = %+v", got)
	}
	if wakes := e.fakes.Waker.Wakes(); len(wakes) != 1 || wakes[0].Agent != "intern" || wakes[0].Reason != core.WakeMessage {
		t.Fatalf("wakes = %+v", wakes)
	}
}

func TestSendBusMessage_RepliesShareTheConversation(t *testing.T) {
	e := newEnv(t)
	createChannel(t, e, builder, "general", "public")
	first := send(t, e, builder, obj{"channel": "general", "text": "Question?"})

	reply := send(t, e, intern, obj{"channel": "general", "text": "Answer.", "reply_to": first.Id})

	if reply.ReplyTo == nil || *reply.ReplyTo != first.Id || reply.CorrelationId == nil || *reply.CorrelationId != *first.CorrelationId {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestSendBusMessage_Refusals(t *testing.T) {
	e := newEnv(t)
	createChannel(t, e, builder, "team.founder", "team")
	tests := []struct {
		name   string
		caller core.Caller
		body   obj
		status int
		typ    string
	}{
		{"neither target", builder, obj{"text": "x"}, http.StatusBadRequest, "validation"},
		{"both targets", builder, obj{"channel": "team.founder", "to": "intern", "text": "x"}, http.StatusBadRequest, "validation"},
		{"blank text", builder, obj{"to": "intern", "text": "   "}, http.StatusBadRequest, "validation"},
		{"unknown recipient", builder, obj{"to": "ghost", "text": "x"}, http.StatusNotFound, "not-found"},
		{"unknown channel", builder, obj{"channel": "nothing", "text": "x"}, http.StatusNotFound, "not-found"},
		{"another branch's channel", outsider, obj{"channel": "team.founder", "text": "x"}, http.StatusNotFound, "not-found"},
		{"a plugin", plugin, obj{"to": "intern", "text": "x"}, http.StatusForbidden, "forbidden"},
	}
	for _, tt := range tests {
		rec := e.do(t, tt.caller, http.MethodPost, "/v1/bus/messages", tt.body)
		if got := problemType(t, rec, tt.status); got != problemBase+tt.typ {
			t.Errorf("%s: problem type = %q", tt.name, got)
		}
	}
}

func TestSendBusMessage_RateLimitAnswers429(t *testing.T) {
	e := newEnv(t)
	for range 120 {
		send(t, e, builder, obj{"to": "intern", "text": "x"})
	}

	rec := e.do(t, builder, http.MethodPost, "/v1/bus/messages", obj{"to": "intern", "text": "x"})

	if got := problemType(t, rec, http.StatusTooManyRequests); got != problemBase+"rate-limited" {
		t.Fatalf("problem type = %q", got)
	}
	e.clock.Advance(time.Minute)
	send(t, e, builder, obj{"to": "intern", "text": "x"})
}

func TestListBusMessages_FiltersAndPagesNewestFirst(t *testing.T) {
	e := newEnv(t)
	createChannel(t, e, builder, "general", "public")
	var sent []api.BusMessage
	for _, text := range []string{"one", "two", "three"} {
		e.clock.Advance(time.Second)
		sent = append(sent, send(t, e, builder, obj{"channel": "general", "text": text}))
	}
	send(t, e, builder, obj{"to": "intern", "text": "private"})

	first := decode[api.BusMessageList](t, e.do(t, human, http.MethodGet, "/v1/bus/messages?channel=general&limit=2", nil), http.StatusOK)
	if len(first.Items) != 2 || first.Items[0].Id != sent[2].Id || first.Items[1].Id != sent[1].Id || first.NextCursor == nil {
		t.Fatalf("first page = %+v", first)
	}
	second := decode[api.BusMessageList](t, e.do(t, human, http.MethodGet, "/v1/bus/messages?channel=general&limit=2&cursor="+*first.NextCursor, nil), http.StatusOK)
	if len(second.Items) != 1 || second.Items[0].Id != sent[0].Id || second.NextCursor != nil {
		t.Fatalf("second page = %+v", second)
	}
	direct := decode[api.BusMessageList](t, e.do(t, human, http.MethodGet, "/v1/bus/messages?to=intern&from=agent:builder", nil), http.StatusOK)
	if len(direct.Items) != 1 || direct.Items[0].Text != "private" {
		t.Fatalf("direct = %+v", direct)
	}
	since := sent[2].CreatedAt
	recent := decode[api.BusMessageList](t, e.do(t, human, http.MethodGet, "/v1/bus/messages?channel=general&since="+time.Time(since).Format(time.RFC3339Nano), nil), http.StatusOK)
	if len(recent.Items) != 1 || recent.Items[0].Id != sent[2].Id {
		t.Fatalf("recent = %+v", recent)
	}
}

func TestListBusMessages_HidesDirectMessagesOfOthers(t *testing.T) {
	e := newEnv(t)
	send(t, e, builder, obj{"to": "intern", "text": "private"})

	mine := decode[api.BusMessageList](t, e.do(t, intern, http.MethodGet, "/v1/bus/messages", nil), http.StatusOK)
	theirs := decode[api.BusMessageList](t, e.do(t, outsider, http.MethodGet, "/v1/bus/messages", nil), http.StatusOK)

	if len(mine.Items) != 1 || len(theirs.Items) != 0 {
		t.Fatalf("recipient sees %d, outsider sees %d", len(mine.Items), len(theirs.Items))
	}
}

func TestListBusMessages_RefusesBadParameters(t *testing.T) {
	e := newEnv(t)
	for _, target := range []string{
		"/v1/bus/messages?cursor=abc",
		"/v1/bus/messages?cursor=0",
		"/v1/bus/messages?from=nobody",
		"/v1/bus/messages?channel=Not+Valid",
	} {
		if got := problemType(t, e.do(t, human, http.MethodGet, target, nil), http.StatusBadRequest); got != problemBase+"validation" {
			t.Errorf("%s: problem type = %q", target, got)
		}
	}
}
