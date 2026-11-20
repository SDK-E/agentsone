// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strconv"
)

// useCommonTimestamp rewrites every time.Time in generated Go source to
// common.Timestamp. The contract spells an instant one way, RFC 3339 in UTC
// with a Z suffix, and common.Timestamp is the one type that encodes it so.
// A plain time.Time keeps the offset it was given, and the generators emit it
// for nullable and inline date-time values.
//
// The time import is dropped when nothing else uses it, and the common import
// is added or reused. Source without time.Time is returned unchanged.
func useCommonTimestamp(src []byte, commonPath string) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse generated source: %w", err)
	}
	type timeUse struct {
		pkg *ast.Ident // the "time" in time.Time
		sel *ast.SelectorExpr
	}
	var timeTimes []timeUse
	otherTimeUses := false
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "time" {
			if sel.Sel.Name == "Time" {
				timeTimes = append(timeTimes, timeUse{pkg: pkg, sel: sel})
			} else {
				otherTimeUses = true
			}
		}
		return true
	})
	if len(timeTimes) == 0 {
		return src, nil
	}

	timeSpec, commonSpec := findImport(file, "time"), findImport(file, commonPath)
	ident := "common"
	if commonSpec != nil && commonSpec.Name != nil {
		ident = commonSpec.Name.Name
	}
	for _, use := range timeTimes {
		use.pkg.Name = ident
		use.sel.Sel.Name = "Timestamp"
	}

	switch {
	case commonSpec == nil && timeSpec != nil && !otherTimeUses:
		timeSpec.Path.Value = strconv.Quote(commonPath)
	case commonSpec == nil:
		file.Decls = append([]ast.Decl{&ast.GenDecl{
			Tok:   token.IMPORT,
			Specs: []ast.Spec{&ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(commonPath)}}},
		}}, file.Decls...)
	case timeSpec != nil && !otherTimeUses:
		removeImport(file, timeSpec)
	}

	var out bytes.Buffer
	if err := format.Node(&out, fset, file); err != nil {
		return nil, fmt.Errorf("print generated source: %w", err)
	}
	return out.Bytes(), nil
}

func findImport(file *ast.File, path string) *ast.ImportSpec {
	for _, spec := range file.Imports {
		if p, err := strconv.Unquote(spec.Path.Value); err == nil && p == path {
			return spec
		}
	}
	return nil
}

func removeImport(file *ast.File, target *ast.ImportSpec) {
	for i, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		for j, spec := range gen.Specs {
			if spec != target {
				continue
			}
			if len(gen.Specs) == 1 {
				file.Decls = append(file.Decls[:i], file.Decls[i+1:]...)
			} else {
				gen.Specs = append(gen.Specs[:j], gen.Specs[j+1:]...)
			}
			return
		}
	}
}
