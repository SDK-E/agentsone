// SPDX-License-Identifier: MIT

// Package bustest holds the repository contracts of the bus context: one
// suite per repository that the in-memory fake and the SQLite adapter must
// both pass, plus builders for test data.
package bustest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// At is the time test data starts at.
var At = time.Date(2026, 11, 19, 10, 0, 0, 123456789, time.UTC)

// ID makes a message id with numeric suffix n.
func ID(n int) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	suffix := []byte("00000000000000000000000000")
	suffix[25] = alphabet[n%32]
	suffix[24] = alphabet[n/32%32]
	return "msg_" + string(suffix)
}

// Channel returns a valid public channel called name.
func Channel(name string) domain.Channel {
	return domain.Channel{
		Name: name, Kind: domain.KindPublic, Description: "Test channel.", CreatedAt: At, CreatedBy: "agent:founder",
	}
}

// TeamChannel returns a valid team channel called name for the branch of team.
func TeamChannel(name, team string) domain.Channel {
	c := Channel(name)
	c.Kind, c.Team = domain.KindTeam, team
	return c
}

// ChannelMessage returns a message number n in channel from agent:founder.
func ChannelMessage(n int, channel string) domain.Message {
	return domain.Message{
		ID: ID(n), Channel: channel, From: "agent:founder", Text: "Hello channel.", CorrelationID: ID(n),
		CreatedAt: At.Add(time.Duration(n) * time.Second),
	}
}

// Direct returns a message number n from agent:from to agent to.
func Direct(n int, from, to string) domain.Message {
	return domain.Message{
		ID: ID(n), To: to, From: "agent:" + from, Text: "Hello agent.", ReplyTo: ID(n - 1), CorrelationID: ID(1),
		CreatedAt: At.Add(time.Duration(n) * time.Second),
	}
}

// RunChannelContract checks a ChannelRepository.
func RunChannelContract(t *testing.T, newRepo func(t *testing.T) domain.ChannelRepository) {
	t.Helper()
	ctx := context.Background()

	t.Run("a stored channel reads back whole", func(t *testing.T) {
		r := newRepo(t)
		want := TeamChannel("team.founder", "founder")
		if err := r.Create(ctx, want); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		got, err := r.Get(ctx, "team.founder")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("Get() = %+v, %v, want %+v", got, err, want)
		}
	})
	t.Run("a taken name is refused", func(t *testing.T) {
		r := newRepo(t)
		if err := r.Create(ctx, Channel("general")); err != nil {
			t.Fatal(err)
		}
		if err := r.Create(ctx, Channel("general")); !errors.Is(err, domain.ErrNameTaken) {
			t.Fatalf("second Create() error = %v, want ErrNameTaken", err)
		}
	})
	t.Run("an unknown channel is not found", func(t *testing.T) {
		r := newRepo(t)
		if _, err := r.Get(ctx, "ghost"); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("Get() error = %v, want ErrNotFound", err)
		}
		if err := r.Touch(ctx, "ghost", At); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("Touch() error = %v, want ErrNotFound", err)
		}
	})
	t.Run("list pages in name order", func(t *testing.T) {
		r := newRepo(t)
		for _, name := range []string{"charlie", "alpha", "bravo"} {
			if err := r.Create(ctx, Channel(name)); err != nil {
				t.Fatal(err)
			}
		}
		first, err := r.List(ctx, "", 2)
		if err != nil || names(first) != "alpha,bravo" {
			t.Fatalf("List(first) = %s, %v", names(first), err)
		}
		rest, err := r.List(ctx, "bravo", 2)
		if err != nil || names(rest) != "charlie" {
			t.Fatalf("List(after bravo) = %s, %v", names(rest), err)
		}
	})
	t.Run("touch records the last message time", func(t *testing.T) {
		r := newRepo(t)
		if err := r.Create(ctx, Channel("general")); err != nil {
			t.Fatal(err)
		}
		at := At.Add(time.Hour)
		if err := r.Touch(ctx, "general", at); err != nil {
			t.Fatal(err)
		}
		got, _ := r.Get(ctx, "general")
		if got.LastMessageAt == nil || !got.LastMessageAt.Equal(at) {
			t.Fatalf("LastMessageAt = %v, want %v", got.LastMessageAt, at)
		}
	})
}

func names(cs []domain.Channel) string {
	out := ""
	for i, c := range cs {
		if i > 0 {
			out += ","
		}
		out += c.Name
	}
	return out
}

func ids(ms []domain.Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

// RunMessageContract checks a MessageRepository.
func RunMessageContract(t *testing.T, newRepo func(t *testing.T) domain.MessageRepository) {
	t.Helper()
	ctx := context.Background()
	fill := func(t *testing.T, r domain.MessageRepository) {
		t.Helper()
		for _, m := range []domain.Message{
			ChannelMessage(1, "general"), Direct(2, "builder", "founder"), ChannelMessage(3, "general"),
			ChannelMessage(4, "ops"), Direct(5, "founder", "builder"),
		} {
			if _, err := r.Append(ctx, m); err != nil {
				t.Fatalf("Append(%s) error = %v", m.ID, err)
			}
		}
	}

	t.Run("append numbers messages from one and returns them whole", func(t *testing.T) {
		r := newRepo(t)
		first, err := r.Append(ctx, Direct(2, "builder", "founder"))
		if err != nil || first.Sequence != 1 {
			t.Fatalf("Append() = %+v, %v, want sequence 1", first, err)
		}
		second, _ := r.Append(ctx, ChannelMessage(3, "general"))
		if second.Sequence != 2 {
			t.Fatalf("second sequence = %d", second.Sequence)
		}
		got, err := r.Get(ctx, ID(2))
		if err != nil || !reflect.DeepEqual(got, first) {
			t.Fatalf("Get() = %+v, %v, want %+v", got, err, first)
		}
	})
	t.Run("a stored id is a conflict", func(t *testing.T) {
		r := newRepo(t)
		fill(t, r)
		if _, err := r.Append(ctx, ChannelMessage(1, "general")); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("Append() error = %v, want ErrConflict", err)
		}
	})
	t.Run("an unknown id is not found", func(t *testing.T) {
		if _, err := newRepo(t).Get(ctx, ID(9)); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("Get() error = %v, want ErrNotFound", err)
		}
	})
	t.Run("before returns the newest first and honours the bound and limit", func(t *testing.T) {
		r := newRepo(t)
		fill(t, r)
		all, err := r.Before(ctx, domain.MessageFilter{}, 0, 10)
		if err != nil || !reflect.DeepEqual(ids(all), []string{ID(5), ID(4), ID(3), ID(2), ID(1)}) {
			t.Fatalf("Before(all) = %v, %v", ids(all), err)
		}
		page, _ := r.Before(ctx, domain.MessageFilter{}, 4, 2)
		if !reflect.DeepEqual(ids(page), []string{ID(3), ID(2)}) {
			t.Fatalf("Before(4, 2) = %v", ids(page))
		}
	})
	t.Run("before filters by channel, recipient, sender and time", func(t *testing.T) {
		r := newRepo(t)
		fill(t, r)
		since := At.Add(3 * time.Second)
		tests := []struct {
			name string
			f    domain.MessageFilter
			want []string
		}{
			{"channel", domain.MessageFilter{Channel: "general"}, []string{ID(3), ID(1)}},
			{"recipient", domain.MessageFilter{To: "builder"}, []string{ID(5)}},
			{"sender", domain.MessageFilter{From: "agent:builder"}, []string{ID(2)}},
			{"since includes the instant", domain.MessageFilter{Since: &since}, []string{ID(5), ID(4), ID(3)}},
			{"filters combine", domain.MessageFilter{Channel: "general", Since: &since}, []string{ID(3)}},
		}
		for _, tt := range tests {
			got, err := r.Before(ctx, tt.f, 0, 10)
			if err != nil || !reflect.DeepEqual(ids(got), tt.want) {
				t.Errorf("%s: Before() = %v, %v, want %v", tt.name, ids(got), err, tt.want)
			}
		}
	})
	t.Run("after returns the oldest first", func(t *testing.T) {
		r := newRepo(t)
		fill(t, r)
		got, err := r.After(ctx, 2, 2)
		if err != nil || !reflect.DeepEqual(ids(got), []string{ID(3), ID(4)}) {
			t.Fatalf("After(2, 2) = %v, %v", ids(got), err)
		}
		none, _ := r.After(ctx, 5, 10)
		if len(none) != 0 {
			t.Fatalf("After(last) = %v, want none", ids(none))
		}
	})
	t.Run("last is the largest sequence", func(t *testing.T) {
		r := newRepo(t)
		if got, err := r.Last(ctx); err != nil || got != 0 {
			t.Fatalf("Last() on an empty log = %d, %v", got, err)
		}
		fill(t, r)
		if got, _ := r.Last(ctx); got != 5 {
			t.Fatalf("Last() = %d, want 5", got)
		}
	})
}
