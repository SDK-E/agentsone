// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/domain"
)

func withUID(uid int) agentstest.Option { return func(a *domain.Agent) { a.UID = uid } }

func hireUID(t *testing.T, agents []domain.Agent) (int, error) {
	t.Helper()
	a, err := domain.NewChart(agents).Hire("founder", hireSpec("mira"), agentstest.At)
	return a.UID, err
}

func TestHire_AllocatesUIDsFromTheAgentRange(t *testing.T) {
	t.Parallel()
	founder := agentstest.New("founder", agentstest.Root())
	tests := []struct {
		name    string
		others  []domain.Agent
		want    int
		wantErr error
	}{
		{"first custom agent", nil, domain.FirstAgentUID, nil},
		{"after the highest uid in use", []domain.Agent{agentstest.New("a", agentstest.Below("founder"), withUID(200007))}, 200008, nil},
		{"never reuses a retired agent's uid", []domain.Agent{
			agentstest.New("old", agentstest.Below("founder"), withUID(200003), agentstest.WithState(domain.StateRetired)),
		}, 200004, nil},
		{"ignores uids outside the range", []domain.Agent{agentstest.New("human-like", agentstest.Below("founder"), withUID(1000))}, domain.FirstAgentUID, nil},
		{"uses the last uid of the range", []domain.Agent{agentstest.New("a", agentstest.Below("founder"), withUID(domain.LastAgentUID-1))}, domain.LastAgentUID, nil},
		{"refuses once the range is used up", []domain.Agent{agentstest.New("a", agentstest.Below("founder"), withUID(domain.LastAgentUID))}, 0, domain.ErrUIDsExhausted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := hireUID(t, append([]domain.Agent{founder}, tt.others...))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Hire() error = %v, want %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("uid = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestUIDRange_IsTheDedicatedAgentRange(t *testing.T) {
	t.Parallel()
	if domain.FirstAgentUID != 200000 || domain.LastAgentUID != 299999 {
		t.Fatalf("range %d..%d, want 200000..299999", domain.FirstAgentUID, domain.LastAgentUID)
	}
}
