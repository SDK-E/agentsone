// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/auth/domain"
)

func TestThrottle_LocksANameAfterTooManyFailures(t *testing.T) {
	t.Parallel()
	th := domain.NewThrottle()
	for i := range domain.MaxFailures {
		if d := th.RetryAfter("ada", epoch); d != 0 {
			t.Fatalf("locked after %d failures", i)
		}
		th.Fail("ada", epoch)
	}
	if d := th.RetryAfter("ada", epoch.Add(10*time.Second)); d != 50*time.Second {
		t.Fatalf("retry after %v, want 50s", d)
	}
	if d := th.RetryAfter("bob", epoch); d != 0 {
		t.Fatalf("other name locked: %v", d)
	}
}

func TestThrottle_UnlocksWhenTheWindowPasses(t *testing.T) {
	t.Parallel()
	th := domain.NewThrottle()
	for range domain.MaxFailures {
		th.Fail("ada", epoch)
	}
	if d := th.RetryAfter("ada", epoch.Add(domain.FailureWindow)); d != 0 {
		t.Fatalf("still locked: %v", d)
	}
}

func TestThrottle_SuccessClearsFailures(t *testing.T) {
	t.Parallel()
	th := domain.NewThrottle()
	for range domain.MaxFailures - 1 {
		th.Fail("ada", epoch)
	}
	th.Succeed("ada")
	th.Fail("ada", epoch)
	if d := th.RetryAfter("ada", epoch); d != 0 {
		t.Fatalf("locked after success: %v", d)
	}
}
