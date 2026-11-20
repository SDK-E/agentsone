// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/emitkit/domain"
)

func TestNewDraft_EncodesData(t *testing.T) {
	t.Parallel()
	d, err := domain.NewDraft("agentsone.clock.tick", "", map[string]string{"minute": "2026-11-20T00:01:00Z"})
	if err != nil {
		t.Fatalf("NewDraft error = %v", err)
	}
	if got, want := string(d.Data), `{"minute":"2026-11-20T00:01:00Z"}`; got != want {
		t.Fatalf("Data = %s, want %s", got, want)
	}
}

func TestNewDraft_RejectsBadInput(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		eventType string
		subject   string
		data      any
	}{
		"type outside the namespace": {"clock.tick", "", map[string]int{}},
		"unencodable data":           {"agentsone.clock.tick", "", make(chan int)},
		"data that is not an object": {"agentsone.clock.tick", "", []int{1}},
		"subject too long":           {"agentsone.clock.tick", strings.Repeat("x", 1025), map[string]int{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.NewDraft(tc.eventType, tc.subject, tc.data); !errors.Is(err, domain.ErrInvalidDraft) {
				t.Fatalf("error = %v, want ErrInvalidDraft", err)
			}
		})
	}
}
