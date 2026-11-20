// SPDX-License-Identifier: MIT

package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/runners/conformance"
	"github.com/SDK-E/agentsone/runners/sdk"
)

// TestEngineErrors maps the engine's failed results and a crash to plain
// turn errors, and a crashed engine is restarted with --resume.
func TestEngineErrors(t *testing.T) {
	for _, tc := range []struct {
		name, mode, prompt, code, message string
	}{
		{"rate limited", "", "RATE", "1030", "The model gateway is limiting calls; try again later"},
		{"upstream fault", "", "BROKEN", "-32603", "The Claude engine ended the turn with an error (success, gateway status 502)"},
		// The last stderr line follows; under -cover it is Go's coverage warning.
		{"crash", "crash", "Hello", "-32603", "The Claude engine stopped unexpectedly: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, nil)
			r.mode = tc.mode
			h := conformance.Ready(t, r.subject(), nil)
			_, msgs := h.RunTurn(tc.prompt)
			e := errorMsg(t, msgs)
			if e.Field("code") != tc.code || !strings.HasPrefix(strings.ToLower(e.Field("message")), strings.ToLower(tc.message)) {
				t.Fatalf("error wrong: %s", e)
			}
			if tc.mode != "crash" {
				return
			}
			if !slices.Contains(r.logLines(), "engine: fake engine crashed") {
				t.Fatalf("the crash is not in the log: %q", r.logLines())
			}
			h.RunTurn("Hello")
			recs := r.records(t)
			if len(recs) != 2 || recs[1].flag("--resume") != recs[0].flag("--session-id") {
				t.Fatalf("a crashed engine must restart with --resume: %v", recs)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	t.Parallel()
	note := "check the disks"
	for _, tc := range []struct {
		wake        sdk.Wake
		long, short string
	}{
		{sdk.Wake{Reason: sdk.WakeChat, Chat: &sdk.ChatMessage{From: "human:alice", Text: "hi"}}, "human:alice says: hi", "answering human:alice"},
		{sdk.Wake{Reason: sdk.WakeMessage, Message: &sdk.BusMessage{From: "agent:bob", Subject: "s", Body: "b"}}, `Message from agent:bob, subject "s": b`, "reading a message from agent:bob"},
		{sdk.Wake{Reason: sdk.WakeTask, Task: &sdk.Task{From: "agent:bob", Title: "T", Body: "B"}}, "Task from agent:bob: T\nB", "working on task T"},
		{sdk.Wake{Reason: sdk.WakeEvent, Event: &sdk.Event{Type: "x.y", Data: map[string]any{"a": 1}}}, `Event x.y: {"a":1}`, "handling event x.y"},
		{sdk.Wake{Reason: sdk.WakeTimer, Timer: &sdk.Timer{Name: "daily", Note: &note}}, "Timer daily fired. check the disks", "handling timer daily"},
		{sdk.Wake{Reason: sdk.WakeTimer, Timer: &sdk.Timer{Name: "daily"}}, "Timer daily fired.", "handling timer daily"},
		{sdk.Wake{Reason: "other"}, "You were woken.", "starting up"},
	} {
		if got := describe(tc.wake); got != tc.long {
			t.Errorf("describe(%s) = %q, want %q", tc.wake.Reason, got, tc.long)
		}
		if got := describeShort(tc.wake); got != tc.short {
			t.Errorf("describeShort(%s) = %q, want %q", tc.wake.Reason, got, tc.short)
		}
	}
}

func TestEngineLineFields(t *testing.T) {
	t.Parallel()
	var l engineLine
	for raw, want := range map[string]string{`"overloaded"`: "overloaded", `{"type":"x"}`: `{"type":"x"}`, `null`: ""} {
		l.Error = json.RawMessage(raw)
		if got := l.errorText(); got != want {
			t.Errorf("errorText(%s) = %q, want %q", raw, got, want)
		}
	}
	l.Error = nil
	if l.errorText() != "" || l.model() != "" || l.toolUses() != nil {
		t.Fatal("an empty line has no error, model or tools")
	}
	if _, ok := (engineLine{Event: json.RawMessage(`{"type":"message_stop"}`)}).textDelta(); ok {
		t.Fatal("message_stop is not a text delta")
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(newUUID()) {
		t.Fatal("newUUID is not a version 4 UUID")
	}
}

func TestQuietSink(t *testing.T) {
	t.Parallel()
	var got []string
	q := quiet{log: func(s string) { got = append(got, s) }}
	if q.Output("x") != nil || q.Usage(sdk.Usage{}) != nil || q.ProfileUpdate("x") != nil || q.Log(sdk.LogInfo, "hello", nil) != nil {
		t.Fatal("the quiet sink never fails")
	}
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("logged %q", got)
	}
}

// TestWriteFiles replaces an existing file with a 0600 one in a 0700
// folder.
func TestWriteFiles(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, mcpConfigFile)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: the wide mode is what the test closes
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil { //nolint:gosec // G306: the wide mode is what the test replaces
		t.Fatal(err)
	}
	if err := writeFiles(map[string][]byte{path: []byte("new")}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, %v", st.Mode(), err)
	}
	if d, _ := os.Stat(dir); d.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode %v", d.Mode())
	}
	if b, _ := os.ReadFile(path); string(b) != "new" { //nolint:gosec // G304: the test's own file
		t.Fatalf("content %q", b)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFiles(map[string][]byte{filepath.Join(blocker, "x"): nil}); err == nil {
		t.Fatal("a folder that is a file must fail")
	}
}
