// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"net/http"
	"testing"

	"github.com/SDK-E/agentsone/internal/daemontest"
)

// SDK-2129: runners are child processes of the daemon, not systemd units, so
// an agent's record leaves `unit` out, as one agent and in the list.
func TestAgentRecord_LeavesTheUnitOut(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")

	var one map[string]any
	alice.Do(http.MethodGet, "/agents/founder", nil, &one)
	if _, ok := one["unit"]; ok {
		t.Errorf("agent record has unit %v, want the field left out", one["unit"])
	}
	if one["name"] != "founder" {
		t.Fatalf("record = %v, want the founder", one)
	}

	var list struct {
		Items []map[string]any `json:"items"`
	}
	alice.Do(http.MethodGet, "/agents", nil, &list)
	if len(list.Items) == 0 {
		t.Fatal("no agents listed")
	}
	for _, a := range list.Items {
		if _, ok := a["unit"]; ok {
			t.Errorf("listed agent %v has unit %v, want the field left out", a["name"], a["unit"])
		}
	}
}
