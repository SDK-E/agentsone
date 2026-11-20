// SPDX-License-Identifier: MIT

package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/mcp/adapters/cli"
)

func render(t *testing.T, body string, asJSON bool) string {
	t.Helper()
	var out bytes.Buffer
	if err := cli.Render(&out, []byte(body), asJSON); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	return out.String()
}

func TestRender_JSONModePassesTheBodyThrough(t *testing.T) {
	t.Parallel()
	got := render(t, `{"name":"bo"}`, true)
	if got != "{\"name\":\"bo\"}\n" {
		t.Fatalf("got %q", got)
	}
}

func TestRender_JSONModeKeepsAnEmptyBodyEmpty(t *testing.T) {
	t.Parallel()
	if got := render(t, "", true); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestRender_EmptyBodyIsOK(t *testing.T) {
	t.Parallel()
	if got := render(t, "  ", false); got != "ok\n" {
		t.Fatalf("got %q", got)
	}
}

func TestRender_ObjectIsAlignedKeyValueLines(t *testing.T) {
	t.Parallel()
	got := render(t, `{"name":"bo","kind":"chat","limits":{"cpu":2},"count":12,"live":true,"note":null}`, false)

	for _, want := range []string{"name", "bo", "kind", "chat", `{"cpu":2}`, "12", "true"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q misses %q", got, want)
		}
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if !strings.HasPrefix(lines[0], "name") || !strings.HasPrefix(lines[1], "kind") {
		t.Errorf("name and kind come first, got %q", lines)
	}
	if strings.Contains(got, "note") {
		t.Errorf("null values are left out, got %q", got)
	}
}

func TestRender_ArrayOfObjectsIsATable(t *testing.T) {
	t.Parallel()
	got := render(t, `[{"name":"bo","kind":"chat","id":1},{"name":"mira","kind":"openai","id":2}]`, false)

	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines: %q", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[0], "NAME") || !strings.Contains(lines[0], "KIND") {
		t.Errorf("header = %q, want id first then name and kind", lines[0])
	}
	if !strings.Contains(lines[2], "mira") || !strings.Contains(lines[2], "openai") {
		t.Errorf("row = %q", lines[2])
	}
}

func TestRender_ListEnvelopeIsATableWithItsOtherFieldsBelow(t *testing.T) {
	t.Parallel()
	got := render(t, `{"items":[{"name":"bo"}],"next_cursor":"abc"}`, false)

	if !strings.Contains(got, "NAME") || !strings.Contains(got, "bo") {
		t.Errorf("no table in %q", got)
	}
	if !strings.Contains(got, "next_cursor: abc") {
		t.Errorf("no footer in %q", got)
	}
}

func TestRender_EmptyListSaysSo(t *testing.T) {
	t.Parallel()
	if got := render(t, `{"items":[]}`, false); got != "no results\n" {
		t.Fatalf("got %q", got)
	}
	if got := render(t, `[]`, false); got != "no results\n" {
		t.Fatalf("got %q", got)
	}
}

func TestRender_TableCellsStayOneShortLine(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 200)
	got := render(t, `[{"name":"bo","text":"`+long+`\nsecond","meta":{"a":1}}]`, false)

	if strings.Contains(got, "second") || strings.Contains(got, long) {
		t.Errorf("a cell was not shortened: %q", got)
	}
	if !strings.Contains(got, "...") || !strings.Contains(got, `{"a":1}`) {
		t.Errorf("got %q", got)
	}
}

func TestRender_ScalarAndOddBodiesPrintPlainly(t *testing.T) {
	t.Parallel()
	if got := render(t, `"hello"`, false); got != "hello\n" {
		t.Errorf("string: %q", got)
	}
	if got := render(t, `42`, false); got != "42\n" {
		t.Errorf("number: %q", got)
	}
	if got := render(t, `["a","b"]`, false); got != "a\nb\n" {
		t.Errorf("string list: %q", got)
	}
	if got := render(t, `not json`, false); got != "not json\n" {
		t.Errorf("text: %q", got)
	}
}

// liveList is a live list as the daemon answers it, built from the generated types.
func liveList(t *testing.T, text string) []byte {
	t.Helper()
	list := api.LiveList{Items: []api.LiveEntry{{
		Agent: "bo", DisplayName: "Bo", Kind: api.AgentKindChat, Role: "Echoes", State: api.AgentStateAwake, Team: "founder",
		OneLine: &api.AgentOneLine{Text: text, UpdatedAt: api.Timestamp(time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC))},
	}, {
		Agent: "mira", DisplayName: "Mira", Kind: api.AgentKindChat, Role: "Router", State: api.AgentStateSleeping, Team: "founder",
	}}}
	b, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("encode the live list: %v", err)
	}
	return b
}

func renderAt(t *testing.T, body []byte, width int) string {
	t.Helper()
	var out bytes.Buffer
	if err := cli.RenderWidth(&out, body, false, width); err != nil {
		t.Fatalf("RenderWidth() error = %v", err)
	}
	return out.String()
}

func TestRender_LiveListShowsTheOneLineAsText(t *testing.T) {
	t.Parallel()
	got := renderAt(t, liveList(t, "Keeping the disk under 80 percent"), 100)

	if !strings.Contains(got, "Keeping the disk under 80 percent") {
		t.Errorf("the one-line text is missing: %q", got)
	}
	for _, raw := range []string{`{"text"`, "updated_at", `"text":`} {
		if strings.Contains(got, raw) {
			t.Errorf("the one-line is printed as JSON (%s): %q", raw, got)
		}
	}
}

func TestRender_OneLineIsCutToFitTheLine(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("watching the disk ", 12)
	for _, width := range []int{60, 80, 120} {
		got := renderAt(t, liveList(t, long), width)
		for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
			if n := utf8.RuneCountInString(line); n > width {
				t.Errorf("width %d: a line is %d wide: %q", width, n, line)
			}
		}
		if !strings.Contains(got, "...") {
			t.Errorf("width %d: the cut is not marked: %q", width, got)
		}
	}
}

func TestRender_ShortOneLineIsNotCutOnAWideLine(t *testing.T) {
	t.Parallel()
	text := "Triage the inbox and answer the urgent mail first, then the rest"
	if got := renderAt(t, liveList(t, text), 160); !strings.Contains(got, text) {
		t.Errorf("a one-line that fits was cut: %q", got)
	}
}

func TestRender_AnAgentWithNoOneLineHasAnEmptyCell(t *testing.T) {
	t.Parallel()
	got := renderAt(t, liveList(t, "Working"), 100)
	if !strings.Contains(got, "mira") || strings.Contains(got, "null") {
		t.Errorf("an agent without a one-line is shown wrongly: %q", got)
	}
}

func TestRender_AnAgentShowsItsOneLineAsText(t *testing.T) {
	t.Parallel()
	body := `{"name":"bo","one_line":{"text":"Answering chat","updated_at":"2026-11-20T05:00:00Z"}}`
	got := render(t, body, false)
	if !strings.Contains(got, "Answering chat") || strings.Contains(got, `"text"`) {
		t.Errorf("got %q", got)
	}
}

func TestRender_JSONModeKeepsTheFullOneLine(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("watching the disk ", 12)
	body := liveList(t, long)
	var out bytes.Buffer
	if err := cli.RenderWidth(&out, body, true, 40); err != nil {
		t.Fatalf("RenderWidth() error = %v", err)
	}
	var back api.LiveList
	if err := json.Unmarshal(out.Bytes(), &back); err != nil {
		t.Fatalf("--json output is not the live list: %v", err)
	}
	if len(back.Items) != 2 || back.Items[0].OneLine == nil || back.Items[0].OneLine.Text != long {
		t.Errorf("--json lost the one-line: %q", out.String())
	}
}
