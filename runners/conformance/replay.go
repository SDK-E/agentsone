// SPDX-License-Identifier: MIT

package conformance

import (
	"bytes"
	"time"
)

// daemonMethods are the requests the daemon sends. Any other request in a
// transcript comes from the runner.
var daemonMethods = map[string]bool{"wake": true, "deliver": true, "cancel": true, "sleep": true, "shutdown": true}

// daemonNotifications are the notifications the daemon sends (protocol 1.1).
// Any other notification in a transcript comes from the runner.
var daemonNotifications = map[string]bool{"credentials.update": true}

// ReplayTranscript plays a transcript against a runner. The transcript is an
// NDJSON file in the form of PROTOCOL.md's example (one line per message, both
// directions in one stream). Daemon lines are sent to the runner; every line
// the runner is expected to send is compared byte for byte with what the
// runner really sends. Heartbeats are not replayed: their timing depends on a
// clock the suite does not control.
//
// The direction of a line follows from its content: requests with the method
// of the daemon (wake, deliver, cancel, sleep, shutdown), the daemon's
// notification credentials.update and responses to runner requests come from
// the daemon; everything else comes from the runner.
//
// A runner that makes decisions of its own (a model) cannot be replayed. Use
// ReplayTranscript with a scripted runner, as the SDK tests do.
func ReplayTranscript(t T, s Subject, transcript []byte) {
	t.Helper()
	h := Start(t, s, nil)
	h.Accept()
	runnerOpen := map[int]bool{} // runner requests the daemon has not answered yet
	for n, line := range bytes.Split(bytes.TrimSpace(transcript), []byte("\n")) {
		line = bytes.TrimSpace(line)
		m, err := parseMsg(append(append([]byte{}, line...), '\n'))
		if err != nil {
			t.Fatalf("transcript line %d: %v", n+1, err)
		}
		if m.Is("heartbeat") {
			continue
		}
		if fromDaemon(m, runnerOpen) {
			if m.IsRequest() {
				h.open[*m.ID] = m.Method
				h.nextReq = *m.ID
			}
			h.SendRaw(m.Raw)
			continue
		}
		got := h.Next()
		if !bytes.Equal(bytes.TrimSpace(got.Raw), line) {
			t.Fatalf("transcript line %d: runner sent\n  %s\nwant\n  %s", n+1, got, line)
		}
		if got.IsRequest() {
			runnerOpen[*got.ID] = true
		}
	}
	h.ReadUntilEOF()
	if code := h.WaitExit(5 * time.Second); code != 0 {
		t.Fatalf("the runner exited with status %d after the transcript, want 0", code)
	}
}

func fromDaemon(m Msg, runnerOpen map[int]bool) bool {
	switch {
	case m.IsRequest():
		return daemonMethods[m.Method]
	case m.IsNotification():
		return daemonNotifications[m.Method]
	case m.IsResponse() && m.ID != nil && runnerOpen[*m.ID]:
		delete(runnerOpen, *m.ID)
		return true
	}
	return false
}
