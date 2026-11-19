// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/memory/domain"
)

func record(scope domain.Scope, owner, team string) domain.Record {
	return domain.Record{ID: "mem_1", Scope: scope, Owner: owner, Team: team}
}

var (
	// The chart has two teams: builder (head) with scribe, and keeper alone.
	builderOwner = domain.Principal{Kind: domain.KindAgent, Name: "builder", Team: "builder"}
	scribe       = domain.Principal{Kind: domain.KindAgent, Name: "scribe", Team: "builder"}
	keeper       = domain.Principal{Kind: domain.KindAgent, Name: "keeper", Team: "keeper"}
	rootAgent    = domain.Principal{Kind: domain.KindAgent, Name: "founder", Team: "founder", Root: true}
	human        = domain.Principal{Kind: domain.KindHuman, Name: "sam"}
	rootHuman    = domain.Principal{Kind: domain.KindHuman, Name: "sam", Root: true}
	system       = domain.Principal{Kind: domain.KindSystem, Name: "host"}
	plugin       = domain.Principal{Kind: domain.KindPlugin, Name: "github"}
)

func TestCanRead_FollowsScopeAndReader(t *testing.T) {
	t.Parallel()
	// Records are written by scribe, whose team head is builder.
	private := record(domain.Private, "scribe", "builder")
	team := record(domain.Team, "scribe", "builder")
	common := record(domain.Common, "scribe", "builder")

	tests := []struct {
		name   string
		reader domain.Principal
		rec    domain.Record
		want   bool
	}{
		{"owner reads own private", scribe, private, true},
		{"team mate reads private", builderOwner, private, false},
		{"outsider reads private", keeper, private, false},
		{"root agent reads private", rootAgent, private, true},

		{"owner reads own team", scribe, team, true},
		{"team mate reads team", builderOwner, team, true},
		{"outsider reads team", keeper, team, false},
		{"root agent reads team", rootAgent, team, true},

		{"owner reads own common", scribe, common, true},
		{"team mate reads common", builderOwner, common, true},
		{"outsider reads common", keeper, common, true},
		{"root agent reads common", rootAgent, common, true},

		{"human reads private", human, private, true},
		{"human reads team", human, team, true},
		{"root human reads private", rootHuman, private, true},
		{"system reads private", system, private, true},

		{"plugin reads private", plugin, private, false},
		{"plugin reads team", plugin, team, false},
		{"plugin reads common", plugin, common, true},

		{"agent without a team reads another team", domain.Principal{Kind: domain.KindAgent, Name: "x"}, record(domain.Team, "y", ""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := domain.CanRead(tt.reader, tt.rec); got != tt.want {
				t.Fatalf("CanRead() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVisibilityOf_AllowsMatchesCanRead(t *testing.T) {
	t.Parallel()
	for _, p := range []domain.Principal{scribe, keeper, rootAgent, human, system, plugin} {
		for _, s := range []domain.Scope{domain.Private, domain.Team, domain.Common} {
			r := record(s, "scribe", "builder")
			if domain.VisibilityOf(p).Allows(r) != domain.CanRead(p, r) {
				t.Fatalf("%s reading %s disagrees", p.Name, s)
			}
		}
	}
}

func TestCanWrite_OnlyAgentsWriteTheirOwnRecords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		writer  domain.Principal
		scope   domain.Scope
		wantErr error
	}{
		{"agent writes own private", scribe, domain.Private, nil},
		{"agent writes own team", scribe, domain.Team, nil},
		{"agent writes common", scribe, domain.Common, nil},
		{"root agent writes private", rootAgent, domain.Private, nil},
		{"human writes common", human, domain.Common, domain.ErrNotAllowed},
		{"plugin writes common", plugin, domain.Common, domain.ErrNotAllowed},
		{"system writes private", system, domain.Private, domain.ErrNotAllowed},
		{"agent without a team writes team", domain.Principal{Kind: domain.KindAgent, Name: "x"}, domain.Team, domain.ErrNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := domain.CanWrite(tt.writer, tt.scope)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CanWrite() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCanDelete_OwnersAndRootOnly(t *testing.T) {
	t.Parallel()
	rec := record(domain.Common, "scribe", "builder")
	tests := []struct {
		name    string
		by      domain.Principal
		wantErr error
	}{
		{"owner deletes", scribe, nil},
		{"team mate deletes", builderOwner, domain.ErrNotAllowed},
		{"outsider deletes", keeper, domain.ErrNotAllowed},
		{"root agent deletes", rootAgent, nil},
		{"root human deletes", rootHuman, nil},
		{"human without root deletes", human, domain.ErrNotAllowed},
		{"plugin deletes", plugin, domain.ErrNotAllowed},
		{"system deletes", system, domain.ErrNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := domain.CanDelete(tt.by, rec)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CanDelete() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
