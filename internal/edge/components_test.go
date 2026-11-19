// SPDX-License-Identifier: MIT

package edge_test

import (
	"slices"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge"
)

// The order is a security property: the audit sink must share env.Proof before anything records,
// and the freeze-all switch must replace the filter's freezer before the parts that gate on it
// (the model gateway, the door).
func TestComponents_StartInTheOrderTheyShareThings(t *testing.T) {
	t.Parallel()
	var names []string
	for _, c := range edge.Components() {
		names = append(names, c.Name())
	}
	want := []string{"audit", "internet filter", "freeze", "model gateway", "door"}
	if !slices.Equal(names, want) {
		t.Fatalf("components = %v, want %v", names, want)
	}
}
