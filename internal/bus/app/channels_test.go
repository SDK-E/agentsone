// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func TestCreateChannel_PublicChannel(t *testing.T) {
	f := newFixture(t)

	c, err := f.svc.CreateChannel(asAgent("intern"), channel("general"))

	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if c.Name != "general" || c.Kind != domain.KindPublic || c.Team != "" || c.CreatedBy != "agent:intern" ||
		!c.CreatedAt.Equal(f.clock.Now()) || c.LastMessageAt != nil {
		t.Fatalf("channel = %+v", c)
	}
	entries := f.auditEntries()
	if len(entries) != 1 || entries[0].Action != "channel.create" || entries[0].Actor != "agent:intern" || entries[0].Target != "channel:general" {
		t.Fatalf("audit = %+v", entries)
	}
}

func TestCreateChannel_TeamChannelBelongsToTheCreatorsBranch(t *testing.T) {
	f := newFixture(t)

	c := mustChannel(asAgent("intern"), t, f, teamChannel("team.founder"))

	if c.Kind != domain.KindTeam || c.Team != "founder" {
		t.Fatalf("channel = %+v, want a team channel for founder", c)
	}
}

func TestCreateChannel_Refusals(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("general"))
	tests := []struct {
		name    string
		ctx     context.Context
		spec    domain.ChannelSpec
		wantErr error
	}{
		{"name taken", asAgent("builder"), channel("general"), core.ErrConflict},
		{"bad name", asAgent("builder"), channel("Bad Name"), core.ErrInvalid},
		{"a human has no branch", asHuman("ada"), teamChannel("team.human"), core.ErrInvalid},
		{"an agent outside the chart has no branch", asAgent("ghost"), teamChannel("team.ghost"), core.ErrNotFound},
		{"unknown kind", asAgent("builder"), domain.ChannelSpec{Name: "x", Kind: "secret"}, core.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := f.svc.CreateChannel(tt.ctx, tt.spec); !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreateChannel() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
	if got := len(f.auditEntries()); got != 1 {
		t.Fatalf("audit entries = %d, want only the successful create", got)
	}
}

func TestCreateChannel_APluginMayCreateAPublicChannel(t *testing.T) {
	f := newFixture(t)

	c, err := f.svc.CreateChannel(asPlugin(), channel("plugin-news"))

	if err != nil || c.CreatedBy != "plugin:plg_01JD7ZK3M00000000000000001" {
		t.Fatalf("CreateChannel() = %+v, %v", c, err)
	}
	if _, err := f.svc.CreateChannel(asPlugin(), teamChannel("plugin-team")); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("plugin team channel error = %v, want ErrInvalid", err)
	}
}

func TestGetChannel_TeamChannelsAreOutOfSightForOtherBranches(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, teamChannel("team.founder"))
	mustChannel(asAgent("outsider"), t, f, channel("general"))
	tests := []struct {
		name    string
		ctx     func() error
		wantErr error
	}{
		{"same branch", func() error { _, err := f.svc.GetChannel(asAgent("intern"), "team.founder"); return err }, nil},
		{"human", func() error { _, err := f.svc.GetChannel(asHuman("ada"), "team.founder"); return err }, nil},
		{"root agent", func() error { _, err := f.svc.GetChannel(asRootAgent("rooty"), "team.founder"); return err }, nil},
		{"other branch", func() error { _, err := f.svc.GetChannel(asAgent("outsider"), "team.founder"); return err }, core.ErrNotFound},
		{"plugin", func() error { _, err := f.svc.GetChannel(asPlugin(), "team.founder"); return err }, core.ErrNotFound},
		{"public for anyone", func() error { _, err := f.svc.GetChannel(asAgent("intern"), "general"); return err }, nil},
		{"unknown", func() error { _, err := f.svc.GetChannel(asAgent("intern"), "ghost"); return err }, core.ErrNotFound},
	}
	for _, tt := range tests {
		if err := tt.ctx(); !errors.Is(err, tt.wantErr) && (tt.wantErr != nil || err != nil) {
			t.Errorf("%s: GetChannel() error = %v, want %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestListChannels_ShowsPublicAndOwnTeamChannelsAndPages(t *testing.T) {
	f := newFixture(t)
	mustChannel(asAgent("builder"), t, f, channel("alpha"))
	mustChannel(asAgent("builder"), t, f, teamChannel("bravo"))
	mustChannel(asAgent("outsider"), t, f, teamChannel("charlie"))
	mustChannel(asAgent("outsider"), t, f, channel("delta"))
	names := func(p []domain.Channel) []string {
		var out []string
		for _, c := range p {
			out = append(out, c.Name)
		}
		return out
	}

	human, err := f.svc.ListChannels(asHuman("ada"), "", 10)
	if err != nil || !slices.Equal(names(human.Channels), []string{"alpha", "bravo", "charlie", "delta"}) || human.Next != "" {
		t.Fatalf("human list = %v, next %q, %v", names(human.Channels), human.Next, err)
	}
	intern, err := f.svc.ListChannels(asAgent("intern"), "", 10)
	if err != nil || !slices.Equal(names(intern.Channels), []string{"alpha", "bravo", "delta"}) {
		t.Fatalf("intern list = %v, %v", names(intern.Channels), err)
	}
	first, err := f.svc.ListChannels(asAgent("intern"), "", 2)
	if err != nil || !slices.Equal(names(first.Channels), []string{"alpha", "bravo"}) || first.Next != "bravo" {
		t.Fatalf("first page = %v, next %q, %v", names(first.Channels), first.Next, err)
	}
	second, err := f.svc.ListChannels(asAgent("intern"), first.Next, 2)
	if err != nil || !slices.Equal(names(second.Channels), []string{"delta"}) || second.Next != "" {
		t.Fatalf("second page = %v, next %q, %v", names(second.Channels), second.Next, err)
	}
}
