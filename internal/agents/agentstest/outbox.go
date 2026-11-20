// SPDX-License-Identifier: MIT

package agentstest

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/domain"
)

// Outbound returns a pending hired event for agent, with a fixed id.
func Outbound(id, agent string, at time.Time) domain.Outbound {
	return domain.Outbound{
		ID: id, Source: "/agents/founder", Actor: "agent:founder", ChartScope: "team:founder",
		Type: "agentsone.agents.hired", Data: json.RawMessage(`{"agent":"` + agent + `"}`), CreatedAt: at.UTC(),
	}
}

func pending(t *testing.T, repo domain.Repository) []domain.Outbound {
	t.Helper()
	got, err := repo.PendingEvents(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func runOutboxContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	at := time.Date(2026, 11, 20, 4, 0, 0, 7, time.UTC)
	t.Run("has no pending events at first", func(t *testing.T) {
		t.Parallel()
		if got := pending(t, newRepo(t)); len(got) != 0 {
			t.Fatalf("PendingEvents() = %v, want none", got)
		}
	})
	t.Run("stores the agent and its event together", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		ev := Outbound("evt_1", "builder", at)
		if err := repo.HireAgent(t.Context(), fullAgent(), ev); err != nil {
			t.Fatalf("HireAgent() error = %v", err)
		}
		if _, err := repo.GetAgent(t.Context(), "builder"); err != nil {
			t.Fatalf("GetAgent() error = %v", err)
		}
		if diff := cmp.Diff([]domain.Outbound{ev}, pending(t, repo)); diff != "" {
			t.Fatalf("pending (-want +got):\n%s", diff)
		}
	})
	t.Run("stores neither when the name is taken", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		createAgents(t, repo, fullAgent())
		err := repo.HireAgent(t.Context(), fullAgent(), Outbound("evt_1", "builder", at))
		if !errors.Is(err, domain.ErrNameTaken) {
			t.Fatalf("HireAgent() error = %v, want %v", err, domain.ErrNameTaken)
		}
		if got := pending(t, repo); len(got) != 0 {
			t.Fatalf("pending after a refused hire = %v, want none", got)
		}
	})
	t.Run("stores neither when the event id is taken", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		if err := repo.HireAgent(t.Context(), New("dev"), Outbound("evt_1", "dev", at)); err != nil {
			t.Fatal(err)
		}
		if err := repo.HireAgent(t.Context(), New("ops"), Outbound("evt_1", "ops", at)); err == nil {
			t.Fatal("HireAgent() with a taken event id returned no error")
		}
		if _, err := repo.GetAgent(t.Context(), "ops"); !errors.Is(err, domain.ErrAgentNotFound) {
			t.Fatalf("agent stored without its event: GetAgent() error = %v", err)
		}
	})
	t.Run("lists pending events oldest first, up to the limit", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		for i, name := range []string{"cy", "ab", "bo"} {
			ev := Outbound("evt_"+name, name, at.Add(time.Duration(i)*time.Second))
			if err := repo.HireAgent(t.Context(), New(name), ev); err != nil {
				t.Fatal(err)
			}
		}
		got, err := repo.PendingEvents(t.Context(), 2)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(got))
		for _, ev := range got {
			ids = append(ids, ev.ID)
		}
		if diff := cmp.Diff([]string{"evt_cy", "evt_ab"}, ids); diff != "" {
			t.Fatalf("PendingEvents(2) ids (-want +got):\n%s", diff)
		}
	})
	t.Run("a sent event is no longer pending", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)
		if err := repo.HireAgent(t.Context(), New("dev"), Outbound("evt_1", "dev", at)); err != nil {
			t.Fatal(err)
		}
		for range 2 { // marking twice is safe: a resend can race a flush
			if err := repo.MarkSent(t.Context(), "evt_1", at.Add(time.Second)); err != nil {
				t.Fatalf("MarkSent() error = %v", err)
			}
		}
		if got := pending(t, repo); len(got) != 0 {
			t.Fatalf("pending after MarkSent = %v, want none", got)
		}
	})
	t.Run("marking an unknown event sent is refused", func(t *testing.T) {
		t.Parallel()
		err := newRepo(t).MarkSent(t.Context(), "evt_ghost", at)
		if !errors.Is(err, domain.ErrEventNotFound) {
			t.Fatalf("MarkSent() error = %v, want %v", err, domain.ErrEventNotFound)
		}
	})
}
