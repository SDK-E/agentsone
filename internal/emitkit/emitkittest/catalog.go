// SPDX-License-Identifier: MIT

package emitkittest

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/emitkit/domain"
	"github.com/SDK-E/agentsone/internal/events/adapters/catalog"
)

// RequireInCatalog fails the test unless every draft is a built-in event type
// of the named emitter whose data matches the contract's schema. It is how a
// source proves it speaks the contract, not just its own idea of it.
func RequireInCatalog(t *testing.T, emitter string, drafts []domain.Draft) {
	t.Helper()
	cat, err := catalog.Builtin()
	if err != nil {
		t.Fatalf("load event catalog: %v", err)
	}
	for _, d := range drafts {
		owner, ok := cat.EmitterOf(d.Type)
		if !ok || owner != emitter {
			t.Errorf("type %s belongs to emitter %q (known: %v), want %q", d.Type, owner, ok, emitter)
			continue
		}
		if err := cat.ValidateData(d.Type, d.Data); err != nil {
			t.Errorf("data of %s does not match the contract: %v (%s)", d.Type, err, d.Data)
		}
	}
}
