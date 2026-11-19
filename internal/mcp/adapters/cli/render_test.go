// SPDX-License-Identifier: MIT

package cli_test

import (
	"bytes"
	"strings"
	"testing"

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
