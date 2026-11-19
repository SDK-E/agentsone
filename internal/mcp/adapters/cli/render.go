// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

const (
	maxCell    = 40
	maxColumns = 7
)

// listKey is the property that holds the rows in the contract's list envelopes.
const listKey = "items"

// firstKeys lead every table and object view, in this order.
var firstKeys = []string{"id", "name", "kind", "status", "state"}

// Render writes an API response body for a person. With asJSON the body goes
// out as it came. Otherwise lists become tables and objects become aligned
// key-value lines.
func Render(w io.Writer, body []byte, asJSON bool) error {
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
		return renderList(w, t, nil)
	case map[string]any:
		if rows, ok := t[listKey].([]any); ok {
			return renderList(w, rows, t)
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
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", k, cell(obj[k], 0)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func renderList(w io.Writer, rows []any, envelope map[string]any) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no results")
		return err
	}
	if err := renderRows(w, rows); err != nil {
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

func renderRows(w io.Writer, rows []any) error {
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
	cols := columns(objects)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
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
			cells[i] = cell(o[c], maxCell)
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
	return keys
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
