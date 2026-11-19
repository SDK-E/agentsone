// SPDX-License-Identifier: MIT

package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/agents/adapters/memory"
	"github.com/SDK-E/agentsone/internal/agents/agentstest"
	"github.com/SDK-E/agentsone/internal/agents/domain"
)

func TestRepositoryContract(t *testing.T) {
	t.Parallel()
	agentstest.RunRepositoryContract(t, func(*testing.T) domain.Repository { return memory.New() })
}

func TestFailNext_FailsOneAgentWriteAndStoresNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := memory.New()
	boom := errors.New("disk full")
	repo.FailNext(boom)

	err := repo.CreateAgent(ctx, agentstest.New("dev"))

	if !errors.Is(err, boom) {
		t.Fatalf("CreateAgent() error = %v, want %v", err, boom)
	}
	if _, err := repo.GetAgent(ctx, "dev"); !errors.Is(err, domain.ErrAgentNotFound) {
		t.Fatalf("agent stored after injected failure: %v", err)
	}
	if err := repo.CreateAgent(ctx, agentstest.New("dev")); err != nil {
		t.Fatalf("second CreateAgent() error = %v, want nil", err)
	}
	repo.FailNext(boom)
	if err := repo.SaveAgents(ctx, agentstest.New("dev")); !errors.Is(err, boom) {
		t.Fatalf("SaveAgents() error = %v, want %v", err, boom)
	}
}
