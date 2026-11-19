// SPDX-License-Identifier: MIT

package conformance

import "testing"

// Run runs every rule against the subject as parallel sub-tests named after
// the rule id, for example TestConformance/echo/T5.
func Run(t *testing.T, s Subject) {
	t.Helper()
	t.Run(s.Name, func(t *testing.T) {
		for _, r := range Rules() {
			t.Run(r.ID, func(t *testing.T) {
				t.Parallel()
				t.Logf("%s: %s", r.ID, r.Title)
				r.Run(t, s)
			})
		}
	})
}
