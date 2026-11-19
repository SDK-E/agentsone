// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func TestProfiles_ListActiveAgentsWithTheirTeams(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if err := e.society.Retire(asAgent("dev"), "intern"); err != nil {
		t.Fatal(err)
	}

	got, err := e.society.Profiles().Profiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var names, teams []string
	for _, p := range got {
		names, teams = append(names, p.Name), append(teams, p.Team)
	}
	if diff := cmp.Diff([]string{"dev", "founder", "lead", "ops", "watcher"}, names); diff != "" {
		t.Errorf("names (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"lead", "founder", "lead", "ops", "ops"}, teams); diff != "" {
		t.Errorf("teams (-want +got):\n%s", diff)
	}
	if got[0].Role != "Worker" || got[0].State != string(domain.StateSleeping) || got[4].Kind != core.AgentWaker {
		t.Errorf("profile fields %+v %+v", got[0], got[4])
	}
}

func TestPostChat_StoresTheAssignmentAndWakesTheAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	router := asAgent("ops")

	id, err := e.society.ChatPoster().PostChat(router, "dev", "Please fix the login", "asg_01JD7ZK3M00000000000000001")
	if err != nil {
		t.Fatal(err)
	}

	list, _ := e.repo.ListChat(context.Background(), "dev", "", 10)
	if len(list) != 1 || list[0].ID != id || list[0].AssignmentID != "asg_01JD7ZK3M00000000000000001" || list[0].Author != "agent:ops" {
		t.Fatalf("history %+v", list)
	}
	if diff := cmp.Diff([]core.WakeReason{core.WakeChat}, e.rt.Wakes()); diff != "" {
		t.Errorf("wakes (-want +got):\n%s", diff)
	}
}

func TestPostChat_RefusesAFrozenAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if err := e.society.Freezer().Freeze(asHost(), scope(t, "agent:dev")); err != nil {
		t.Fatal(err)
	}

	_, err := e.society.ChatPoster().PostChat(asHuman("ada"), "dev", "hi", "")

	if !errors.Is(err, core.ErrFrozen) {
		t.Fatalf("error = %v, want %v", err, core.ErrFrozen)
	}
}
