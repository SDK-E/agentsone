// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

var at = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

func TestValidChannelName(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"general", "team.founder", "a", "0day", "ops-1.eu", strings.Repeat("a", 63)} {
		if !domain.ValidChannelName(ok) {
			t.Errorf("ValidChannelName(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "General", ".general", "-x", "has space", "under_score", strings.Repeat("a", 64)} {
		if domain.ValidChannelName(bad) {
			t.Errorf("ValidChannelName(%q) = true", bad)
		}
	}
}

func TestNewChannel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		spec    domain.ChannelSpec
		team    string
		wantErr error
		want    func(domain.Channel) bool
	}{
		{"public channel has no team", domain.ChannelSpec{Name: "general", Kind: domain.KindPublic, Description: "All hands."}, "founder", nil,
			func(c domain.Channel) bool {
				return c.Team == "" && c.Kind == domain.KindPublic && c.CreatedAt.Equal(at)
			}},
		{"team channel keeps the branch head", domain.ChannelSpec{Name: "team.founder", Kind: domain.KindTeam}, "founder", nil,
			func(c domain.Channel) bool { return c.Team == "founder" && c.LastMessageAt == nil }},
		{"team channel without a branch", domain.ChannelSpec{Name: "team.x", Kind: domain.KindTeam}, "", domain.ErrNoTeam, nil},
		{"bad name", domain.ChannelSpec{Name: "Bad Name", Kind: domain.KindPublic}, "", domain.ErrInvalidChannel, nil},
		{"unknown kind", domain.ChannelSpec{Name: "x", Kind: "secret"}, "", domain.ErrInvalidChannel, nil},
		{"description at 500", domain.ChannelSpec{Name: "x", Kind: domain.KindPublic, Description: strings.Repeat("d", 500)}, "", nil, nil},
		{"description over 500", domain.ChannelSpec{Name: "x", Kind: domain.KindPublic, Description: strings.Repeat("d", 501)}, "", domain.ErrInvalidChannel, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NewChannel(tt.spec, "agent:founder", tt.team, at)
			if !errors.Is(err, tt.wantErr) && (tt.wantErr != nil || err != nil) {
				t.Fatalf("NewChannel() error = %v, want %v", err, tt.wantErr)
			}
			if tt.want != nil && !tt.want(got) {
				t.Fatalf("NewChannel() = %+v", got)
			}
			if tt.wantErr != nil && !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
}

func TestChannelVisibility(t *testing.T) {
	t.Parallel()
	public := domain.Channel{Name: "general", Kind: domain.KindPublic}
	team := domain.Channel{Name: "team.founder", Kind: domain.KindTeam, Team: "founder"}
	tests := []struct {
		name   string
		viewer domain.Viewer
		ch     domain.Channel
		want   bool
	}{
		{"anyone sees a public channel", domain.Viewer{}, public, true},
		{"member sees the team channel", domain.Viewer{Name: "builder", TeamRoot: "founder"}, team, true},
		{"other branch does not", domain.Viewer{Name: "outsider", TeamRoot: "outsider"}, team, false},
		{"viewer without a branch does not", domain.Viewer{}, team, false},
		{"a human sees every channel", domain.Viewer{All: true}, team, true},
	}
	for _, tt := range tests {
		if got := tt.viewer.CanSeeChannel(tt.ch); got != tt.want {
			t.Errorf("%s: CanSeeChannel() = %v, want %v", tt.name, got, tt.want)
		}
	}
}
