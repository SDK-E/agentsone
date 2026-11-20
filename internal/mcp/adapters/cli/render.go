// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/SDK-E/agentsone/internal/contract/api"
)

const (
	maxCell    = 40
	maxColumns = 7
	// defaultWidth is the line width when the output is no terminal and COLUMNS is not set.
	defaultWidth = 100
	// minOneLine is the least room a table gives the one-line column, however narrow the line.
	minOneLine = 20
	// columnGap is the space tabwriter puts between two columns.
	columnGap = 2
)

// oneLineKey is the property that holds an agent's one-line: an object with
// the text and when it was written (api.AgentOneLine).
const oneLineKey = "one_line"

// listKey is the property that holds the rows in the contract's list envelopes.
const listKey = "items"

// firstKeys lead every table and object view, in this order.
var firstKeys = []string{"id", "name", "kind", "status", "state"}

// Render writes an API response body for a person. With asJSON the body goes
// out as it came. Otherwise lists become tables and objects become aligned
// key-value lines. A table is as wide as the terminal w writes to.
func Render(w io.Writer, body []byte, asJSON bool) error {
	return RenderWidth(w, body, asJSON, lineWidth(w))
}

// lineWidth is how many characters fit on one line of w: the terminal's
// width, else COLUMNS, else defaultWidth.
func lineWidth(w io.Writer) int {
	if f, ok := w.(*os.File); ok && term.IsTerminal(int(f.Fd())) { // #nosec G115 -- a file descriptor fits an int
		if cols, _, err := term.GetSize(int(f.Fd())); err == nil && cols > 0 { // #nosec G115 -- a file descriptor fits an int
			return cols
		}
	}
	if cols, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && cols > 0 {
		return cols
	}
	return defaultWidth
}

// RenderWidth is Render for a line that holds width characters. Only a
// table reads the width: it cuts each agent's one-line to the room left.
func RenderWidth(w io.Writer, body []byte, asJSON bool, width int) error {
	trimmed := bytes.TrimSpace(body)
	if asJSON {
		if len(trimmed) == 0 {
			return nil
		}
		_, err := fmt.Fprintf(w, "%s\n", trimmed)
		return err
	}
	if len(trimmed) == 0 {
		_, err := fmt.Fprintln(w, "ok")
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		_, werr := fmt.Fprintf(w, "%s\n", trimmed)
		return werr
	}
	switch t := v.(type) {
	case []any:
		return renderList(w, t, nil, width)
	case map[string]any:
		if rows, ok := t[listKey].([]any); ok {
			return renderList(w, rows, t, width)
		}
		return renderObject(w, t)
	case string:
		_, err := fmt.Fprintln(w, t)
		return err
	}
	_, err := fmt.Fprintf(w, "%s\n", trimmed)
	return err
}

func renderObject(w io.Writer, obj map[string]any) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, k := range orderedKeys(obj) {
		if obj[k] == nil {
			continue
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", k, cellOf(k, obj[k], 0)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func renderList(w io.Writer, rows []any, envelope map[string]any, width int) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no results")
		return err
	}
	if err := renderRows(w, rows, width); err != nil {
		return err
	}
	for _, k := range orderedKeys(envelope) {
		if k == listKey || envelope[k] == nil {
			continue
		}
		if _, err := fmt.Fprintf(w, "%s: %s\n", k, cell(envelope[k], 0)); err != nil {
			return err
		}
	}
	return nil
}

func renderRows(w io.Writer, rows []any, width int) error {
	objects := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		o, ok := r.(map[string]any)
		if !ok {
			// A plain list prints one value per line.
			for _, r := range rows {
				if _, err := fmt.Fprintln(w, cell(r, 0)); err != nil {
					return err
				}
			}
			return nil
		}
		objects = append(objects, o)
	}
	cols, limits := fitColumns(columns(objects), objects, width)
	tw := tabwriter.NewWriter(w, 0, 0, columnGap, ' ', 0)
	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = strings.ToUpper(c)
	}
	if _, err := fmt.Fprintln(tw, strings.Join(head, "\t")); err != nil {
		return err
	}
	for _, o := range objects {
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = cellOf(c, o[c], limits[i])
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// columns picks the table columns: every key that has a value in some row,
// the well-known ones first, at most maxColumns.
func columns(rows []map[string]any) []string {
	seen := map[string]bool{}
	for _, r := range rows {
		for k, v := range r {
			if v != nil {
				seen[k] = true
			}
		}
	}
	all := make(map[string]any, len(seen))
	for k := range seen {
		all[k] = true
	}
	keys := orderedKeys(all)
	if len(keys) > maxColumns {
		keys = keys[:maxColumns]
	}
	// The one-line is the long cell, so it goes last and takes the room the others leave.
	if i := slices.Index(keys, oneLineKey); i >= 0 {
		keys = append(slices.Delete(keys, i, i+1), oneLineKey)
	}
	return keys
}

// fitColumns drops the last columns of a table with a one-line until the
// one-line has minOneLine characters on a line of width characters, and
// returns the columns that stay with each one's cell limit. Every column
// holds maxCell, except the one-line, which holds what is left of the line.
// The first column always stays.
func fitColumns(cols []string, rows []map[string]any, width int) ([]string, []int) {
	for {
		limits := make([]int, len(cols))
		used := 0
		for i, c := range cols {
			limits[i] = maxCell
			if c == oneLineKey {
				continue
			}
			wide := utf8.RuneCountInString(c)
			for _, r := range rows {
				wide = max(wide, utf8.RuneCountInString(cellOf(c, r[c], maxCell)))
			}
			used += wide + columnGap
		}
		i := slices.Index(cols, oneLineKey)
		if i < 0 {
			return cols, limits
		}
		room := width - used
		if room >= minOneLine || len(cols) <= 2 {
			limits[i] = max(room, minOneLine)
			return cols, limits
		}
		cols = slices.Delete(slices.Clone(cols), i-1, i)
	}
}

// cellOf is cell for the property key: an agent's one-line shows its text.
func cellOf(key string, v any, limit int) string {
	if key == oneLineKey {
		if text, ok := oneLineText(v); ok {
			return cell(text, limit)
		}
	}
	return cell(v, limit)
}

// oneLineText reads v as the contract's one-line (api.AgentOneLine) and
// returns its text.
func oneLineText(v any) (string, bool) {
	obj, ok := v.(map[string]any)
	if !ok {
		return "", false
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return "", false
	}
	var one api.AgentOneLine
	if err := json.Unmarshal(raw, &one); err != nil || one.Text == "" {
		return "", false
	}
	return one.Text, true
}

// orderedKeys sorts keys with the well-known ones first.
func orderedKeys(m map[string]any) []string {
	rank := map[string]int{}
	for i, k := range firstKeys {
		rank[k] = i + 1
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := rank[keys[i]], rank[keys[j]]
		switch {
		case ri != 0 && rj != 0:
			return ri < rj
		case ri != 0:
			return true
		case rj != 0:
			return false
		}
		return keys[i] < keys[j]
	})
	return keys
}

// cell writes one value on one line. limit 0 means no cut.
func cell(v any, limit int) string {
	var text string
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		text = strings.Join(strings.Fields(t), " ")
	case json.Number:
		text = t.String()
	case bool:
		text = fmt.Sprint(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		text = string(b)
	}
	if limit > 0 {
		if r := []rune(text); len(r) > limit {
			return string(r[:limit-3]) + "..."
		}
	}
	return text
}
