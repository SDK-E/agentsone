// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/template"
	"unicode"
)

const openRPCPath = "contract/schemas/runner/runner.openrpc.json"

type rpcMethod struct {
	Name    string
	Const   string
	Summary string
}

// generateRunnerMethods writes method name constants from the OpenRPC document.
func generateRunnerMethods(r repo) error {
	raw, err := os.ReadFile(r.path(openRPCPath))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", openRPCPath, err)
	}
	var doc struct {
		Methods []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", openRPCPath, err)
	}
	methods := make([]rpcMethod, 0, len(doc.Methods))
	for _, m := range doc.Methods {
		methods = append(methods, rpcMethod{Name: m.Name, Const: "Method" + goName(m.Name), Summary: oneLine(m.Summary)})
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].Name < methods[j].Name })
	src, err := render(methodsTemplate, methods)
	if err != nil {
		return err
	}
	return r.writeGo(protocolDir+"/methods.gen.go", src)
}

// goName turns "bus.send" or "profile_update" into "BusSend" / "ProfileUpdate".
func goName(s string) string {
	var b strings.Builder
	upper := true
	for _, c := range s {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) {
			upper = true
			continue
		}
		if upper {
			c = unicode.ToUpper(c)
			upper = false
		}
		b.WriteRune(c)
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

var methodsTemplate = template.Must(template.New("methods").Parse(generatedNote + `package protocol

// JSON-RPC method names of the runner protocol.
const (
{{- range .}}
	// {{.Const}}{{if .Summary}}: {{.Summary}}{{end}}
	{{.Const}} = "{{.Name}}"
{{- end}}
)
`))
