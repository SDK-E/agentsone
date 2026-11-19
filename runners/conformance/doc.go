// SPDX-License-Identifier: MIT

// Package conformance is the reusable conformance suite for Agents One
// runners. It plays the daemon side of the runner protocol against a runner
// and checks one rule per test; each test names the rule id from
// contract/schemas/runner/PROTOCOL.md (F1, H2, T5 and so on).
//
// A runner author describes the runner as a Subject and calls Run from a
// test:
//
//	func TestConformance(t *testing.T) {
//		conformance.Run(t, conformance.Subject{
//			Name:    "echo",
//			Start:   conformance.InProcess(runMyRunner), // or conformance.Binary(path)
//			Prompts: conformance.Prompts{Plain: "hello", Hold: "echo:hold", Budget: "echo:budget"},
//		})
//	}
//
// The suite checks the "runner" rules of PROTOCOL.md. The "daemon" rules
// (H3 to H6, B2, P1) are checked against the daemon in its own tests.
// Every line a runner sends is also checked against messages.schema.json,
// the line limit and the request id sequence while any rule runs (F1, F2, F5, F6).
//
// ReplayTranscript plays the daemon lines of a recorded session, such as
// contract/fixtures/schemas/runner-message/transcript.ndjson, and compares the
// runner's lines with the recording.
package conformance
