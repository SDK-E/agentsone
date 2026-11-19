// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"text/template"
)

// timeTypes lists types declared as `type X time.Time` (not aliases). A named
// time type loses time.Time's JSON methods and would encode as {}, so each
// one gets RFC 3339 methods generated next to it.
func timeTypes(src []byte) ([]string, string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		return nil, "", fmt.Errorf("parse generated types: %w", err)
	}
	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok && ts.Assign == 0 && isTimeTime(ts.Type) {
				names = append(names, ts.Name.Name)
			}
		}
	}
	sort.Strings(names)
	return names, file.Name.Name, nil
}

func isTimeTime(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "time" && sel.Sel.Name == "Time"
}

// addTimeMethods writes <dir>/time_json.gen.go for the named time types
// declared in the given generated files of one package.
func addTimeMethods(r repo, dir string, files []string) error {
	var names []string
	pkg := ""
	for _, rel := range files {
		src, err := os.ReadFile(r.path(rel)) // #nosec G304 -- generated file inside the repository
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		found, name, err := timeTypes(src)
		if err != nil {
			return err
		}
		names, pkg = append(names, found...), name
	}
	if len(names) == 0 {
		return nil
	}
	src, err := render(timeMethodsTemplate, map[string]any{"Package": pkg, "Types": names})
	if err != nil {
		return err
	}
	return r.writeGo(dir+"/time_json.gen.go", src)
}

var timeMethodsTemplate = template.Must(template.New("time").Parse(generatedNote + `package {{.Package}}

import "time"
{{range .Types}}
// MarshalJSON writes the time in RFC 3339 form, in UTC.
func (t {{.}}) MarshalJSON() ([]byte, error) { return time.Time(t).UTC().MarshalJSON() }

// UnmarshalJSON reads an RFC 3339 time.
func (t *{{.}}) UnmarshalJSON(b []byte) error {
	var v time.Time
	if err := v.UnmarshalJSON(b); err != nil {
		return err
	}
	*t = {{.}}(v)
	return nil
}
{{end}}`))
