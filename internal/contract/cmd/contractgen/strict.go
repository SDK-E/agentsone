// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
)

type strictMethod struct {
	Name     string
	Request  string
	Response string
}

// strictMethods reads StrictServerInterface from generated server code, so
// Unimplemented uses exactly the names oapi-codegen chose.
func strictMethods(src []byte) ([]strictMethod, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "server.gen.go", src, 0)
	if err != nil {
		return nil, fmt.Errorf("parse generated server: %w", err)
	}
	iface := findInterface(file, "StrictServerInterface")
	if iface == nil {
		return nil, errors.New("generated server has no StrictServerInterface")
	}
	var methods []strictMethod
	for _, field := range iface.Methods.List {
		fn, ok := field.Type.(*ast.FuncType)
		if !ok || len(field.Names) != 1 || len(fn.Params.List) != 2 || len(fn.Results.List) != 2 {
			return nil, fmt.Errorf("unexpected StrictServerInterface method shape at %s", fset.Position(field.Pos()))
		}
		methods = append(methods, strictMethod{
			Name:     field.Names[0].Name,
			Request:  exprString(fset, fn.Params.List[1].Type),
			Response: exprString(fset, fn.Results.List[0].Type),
		})
	}
	return methods, nil
}

func findInterface(file *ast.File, name string) *ast.InterfaceType {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != name {
				continue
			}
			if it, ok := ts.Type.(*ast.InterfaceType); ok {
				return it
			}
		}
	}
	return nil
}

func exprString(fset *token.FileSet, e ast.Expr) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, fset, e) // printing a parsed expression into a buffer cannot fail
	return b.String()
}
