// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

const (
	msgA = "msg_01JD7ZK3M00000000000000001"
	msgB = "msg_01JD7ZK3M00000000000000002"
)

func TestDraft_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		draft   domain.Draft
		wantErr error
	}{
		{"channel message", domain.Draft{Channel: "general", Text: "hi"}, nil},
		{"direct message", domain.Draft{To: "builder", Text: "hi"}, nil},
		{"reply", domain.Draft{To: "builder", Text: "hi", ReplyTo: msgA}, nil},
		{"both channel and to", domain.Draft{Channel: "general", To: "builder", Text: "hi"}, domain.ErrInvalidMessage},
		{"neither channel nor to", domain.Draft{Text: "hi"}, domain.ErrInvalidMessage},
		{"empty text", domain.Draft{To: "builder"}, domain.ErrInvalidMessage},
		{"blank text", domain.Draft{To: "builder", Text: " \n\t"}, domain.ErrInvalidMessage},
		{"text at 16000 characters", domain.Draft{To: "builder", Text: strings.Repeat("é", 16000)}, nil},
		{"text over 16000 characters", domain.Draft{To: "builder", Text: strings.Repeat("é", 16001)}, domain.ErrInvalidMessage},
		{"bad channel name", domain.Draft{Channel: "Bad", Text: "hi"}, domain.ErrInvalidMessage},
		{"bad agent name", domain.Draft{To: "Bad Name", Text: "hi"}, domain.ErrInvalidMessage},
		{"bad reply id", domain.Draft{To: "builder", Text: "hi", ReplyTo: "msg_1"}, domain.ErrInvalidMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.draft.Validate()
			if !errors.Is(err, tt.wantErr) && (tt.wantErr != nil || err != nil) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
}

func TestDraft_Message(t *testing.T) {
	t.Parallel()
	d := domain.Draft{To: "builder", Text: "hi", ReplyTo: msgA}

	m := d.Message(msgB, "agent:founder", msgA, at)

	if m.ID != msgB || m.From != "agent:founder" || m.To != "builder" || m.Channel != "" || m.Text != "hi" ||
		m.ReplyTo != msgA || m.CorrelationID != msgA || !m.CreatedAt.Equal(at) {
		t.Fatalf("Message() = %+v", m)
	}
}

func TestMessage_IsDirect(t *testing.T) {
	t.Parallel()
	if !(domain.Message{To: "builder"}).IsDirect() || (domain.Message{Channel: "general"}).IsDirect() {
		t.Fatal("IsDirect() does not tell direct messages from channel messages")
	}
}

func TestMessage_Subject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want string
	}{
		{"one line", "Please review PR 7.", "Please review PR 7."},
		{"first line only", "Review PR 7\n\nIt touches the bus.", "Review PR 7"},
		{"skips leading blank lines", "\n\n  Review PR 7  \nmore", "Review PR 7"},
		{"crlf", "Review PR 7\r\nmore", "Review PR 7"},
		{"long line is cut with an ellipsis", strings.Repeat("a", 100), strings.Repeat("a", 79) + "…"},
		{"cut counts characters, not bytes", strings.Repeat("é", 100), strings.Repeat("é", 79) + "…"},
		{"exactly the limit stays whole", strings.Repeat("a", 80), strings.Repeat("a", 80)},
		{"blank text still has a subject", " \n ", "(no subject)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := (domain.Message{Text: tt.text}).Subject(); got != tt.want {
				t.Fatalf("Subject() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidActor(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"agent:founder", "human:ada", "system:daemon", "host:agentsone", "plugin:plg_01JD7ZK3M00000000000000001"} {
		if !domain.ValidActor(ok) {
			t.Errorf("ValidActor(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "founder", "agent:", "agent:Founder", "robot:x", "plugin:x"} {
		if domain.ValidActor(bad) {
			t.Errorf("ValidActor(%q) = true", bad)
		}
	}
}

func TestActorOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		caller  core.Caller
		want    string
		wantErr error
	}{
		{"agent", core.Caller{Kind: core.CallerAgent, Name: "scribe"}, "agent:scribe", nil},
		{"human", core.Caller{Kind: core.CallerHuman, Name: "ada"}, "human:ada", nil},
		{"plugin", core.Caller{Kind: core.CallerPlugin, Name: "plg_01JD7ZK3M00000000000000001"}, "plugin:plg_01JD7ZK3M00000000000000001", nil},
		{"bad name", core.Caller{Kind: core.CallerAgent, Name: "Bad"}, "", domain.ErrInvalidActor},
		{"no name", core.Caller{Kind: core.CallerHuman}, "", domain.ErrInvalidActor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ActorOf(tt.caller)
			if !errors.Is(err, tt.wantErr) && (tt.wantErr != nil || err != nil) {
				t.Fatalf("ActorOf() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ActorOf() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		wantCh  string
		wantTo  string
		wantErr bool
	}{
		{"channel:general", "general", "", false},
		{"agent:builder", "", "builder", false},
		{"general", "", "", true},
		{"channel:Bad", "", "", true},
		{"agent:", "", "", true},
		{"human:ada", "", "", true},
	}
	for _, tt := range tests {
		ch, to, err := domain.ParseTarget(tt.in)
		if (err != nil) != tt.wantErr || ch != tt.wantCh || to != tt.wantTo {
			t.Errorf("ParseTarget(%q) = %q, %q, %v", tt.in, ch, to, err)
		}
		if err != nil && !errors.Is(err, core.ErrInvalid) {
			t.Errorf("ParseTarget(%q) error %v does not wrap ErrInvalid", tt.in, err)
		}
	}
}

func TestViewer_CanSeeMessage(t *testing.T) {
	t.Parallel()
	below := func(name string) bool { return name == "intern" } // viewer manages intern
	dm := domain.Message{From: "agent:builder", To: "outsider"}
	tests := []struct {
		name   string
		viewer domain.Viewer
		msg    domain.Message
		want   bool
	}{
		{"sender", domain.Viewer{Name: "builder"}, dm, true},
		{"recipient", domain.Viewer{Name: "outsider"}, dm, true},
		{"stranger", domain.Viewer{Name: "intern"}, dm, false},
		{"manager of the sender", domain.Viewer{Name: "founder", IsBelow: func(n string) bool { return n == "builder" }}, dm, true},
		{"manager of the recipient", domain.Viewer{Name: "boss", IsBelow: func(n string) bool { return n == "outsider" }}, dm, true},
		{"manager of someone else", domain.Viewer{Name: "boss", IsBelow: below}, dm, false},
		{"human sees all", domain.Viewer{All: true}, dm, true},
		{"human sender is not an agent", domain.Viewer{Name: "builder"}, domain.Message{From: "human:builder", To: "outsider"}, false},
	}
	for _, tt := range tests {
		if got := tt.viewer.CanSeeDirect(tt.msg); got != tt.want {
			t.Errorf("%s: CanSeeDirect() = %v, want %v", tt.name, got, tt.want)
		}
	}
}
