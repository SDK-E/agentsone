// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

func TestStatusProgress_IsTheShareOfBytesDone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		s    domain.Status
		want float64
	}{
		{"nothing known yet", domain.Status{State: domain.StateQueued}, 0},
		{"total unknown", domain.Status{State: domain.StateDownloading, BytesDone: 10}, 0},
		{"halfway", domain.Status{State: domain.StateDownloading, BytesDone: 50, BytesTotal: 100}, 0.5},
		{"more than total is capped", domain.Status{State: domain.StateDownloading, BytesDone: 120, BytesTotal: 100}, 1},
		{"ready is always whole", domain.Status{State: domain.StateReady}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.s.Progress(); got != tt.want {
				t.Fatalf("Progress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStateValues_MatchTheContract(t *testing.T) {
	t.Parallel()
	// contract/openapi/components/schemas/setup.yaml EngineFetchState.
	want := []domain.State{"waiting-licence", "queued", "downloading", "verifying", "ready", "failed"}
	got := []domain.State{
		domain.StateWaitingLicence, domain.StateQueued, domain.StateDownloading,
		domain.StateVerifying, domain.StateReady, domain.StateFailed,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("state %d = %q, want %q", i, got[i], want[i])
		}
	}
}
