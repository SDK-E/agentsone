// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

const (
	catalogPath  = "contract/schemas/events/catalog.json"
	eventDataDir = "internal/contract/eventdata"
)

type eventDataType struct {
	Type   string // agentsone.agents.hired
	GoName string // AgentsHired
}

// generateEventData writes one Go type per event data schema in the event
// catalog, and the Data interface only those types implement, so daemon
// modules build event data from the contract and never by hand.
func generateEventData(r repo) error {
	raw, err := os.ReadFile(r.path(catalogPath))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", catalogPath, err)
	}
	var doc struct {
		Events []struct {
			Type       string                     `json:"type"`
			DataSchema map[string]json.RawMessage `json:"dataSchema"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", catalogPath, err)
	}
	sort.Slice(doc.Events, func(i, j int) bool { return doc.Events[i].Type < doc.Events[j].Type })
	tmp, err := os.MkdirTemp("", "contractgen-eventdata-")
	if err != nil {
		return fmt.Errorf("temp folder: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }() //nolint:errcheck // a scratch folder left behind changes nothing in the generated files
	types := make([]eventDataType, 0, len(doc.Events))
	files := make([]string, 0, len(doc.Events))
	for _, e := range doc.Events {
		t := eventDataType{Type: e.Type, GoName: goName(strings.TrimPrefix(e.Type, "agentsone."))}
		schema := e.DataSchema
		schema["$schema"] = json.RawMessage(`"https://json-schema.org/draft/2020-12/schema"`)
		schema["$id"] = jsonString("https://agentsone.dev/contract/events/" + e.Type + ".data.json")
		schema["title"] = jsonString(t.GoName)
		b, err := json.Marshal(schema)
		if err != nil {
			return fmt.Errorf("data schema of %s: %w", e.Type, err)
		}
		p := filepath.Join(tmp, e.Type+".json")
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return fmt.Errorf("write data schema of %s: %w", e.Type, err)
		}
		types = append(types, t)
		files = append(files, p)
	}
	if err := r.removeGenerated(eventDataDir); err != nil {
		return err
	}
	args := append([]string{"go-jsonschema", "--only-models", "--tags", "json", "--capitalization", "ID",
		"--struct-name-from-title", "-p", "eventdata"}, files...)
	models, err := r.goTool(args...)
	if err != nil {
		return err
	}
	if models, err = useCommonTimestamp(models, importPath(commonDir)); err != nil {
		return err
	}
	if err := r.writeGo(eventDataDir+"/models.gen.go", models); err != nil {
		return err
	}
	src, err := render(eventDataTemplate, types)
	if err != nil {
		return err
	}
	return r.writeGo(eventDataDir+"/types.gen.go", src)
}

func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s) //nolint:errcheck // a string always encodes
	return b
}

var eventDataTemplate = template.Must(template.New("eventdata").Parse(generatedNote + `// Package eventdata holds one type per event in the contract event catalog
// (contract/schemas/events/catalog.json). Daemon modules publish only these.
package eventdata

// Data is the data of one catalog event. Only the generated types implement
// it, so a hand-built payload does not compile where Data is asked for.
type Data interface {
	// EventType is the catalog type this data belongs to.
	EventType() string
	eventData()
}

// Event types of the catalog.
const (
{{- range .}}
	Type{{.GoName}} = "{{.Type}}"
{{- end}}
)

// All returns a zero value of every generated type, in type order.
func All() []Data {
	return []Data{
{{- range .}}
		{{.GoName}}{},
{{- end}}
	}
}
{{range .}}
// EventType returns {{.Type}}.
func ({{.GoName}}) EventType() string { return Type{{.GoName}} }

func ({{.GoName}}) eventData() {}
{{end}}`))
