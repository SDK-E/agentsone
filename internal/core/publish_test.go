// SPDX-License-Identifier: MIT

package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/eventdata"
	"github.com/SDK-E/agentsone/internal/core"
)

type recordingBus struct{ got []core.Event }

func (b *recordingBus) Publish(_ context.Context, e core.Event) (core.Event, error) {
	b.got = append(b.got, e)
	return e, nil
}

func TestPublishData_TypeAndDataComeFromTheGeneratedType(t *testing.T) {
	t.Parallel()
	bus := &recordingBus{}

	_, err := core.PublishData(t.Context(), bus, core.Publication{
		Source: "/daemon", Actor: "system:auth", ChartScope: "machine",
		Data: eventdata.AuthLogout{User: "ada"},
	})

	if err != nil {
		t.Fatalf("PublishData() error = %v", err)
	}
	if len(bus.got) != 1 {
		t.Fatalf("published %d events, want 1", len(bus.got))
	}
	e := bus.got[0]
	if e.Type != eventdata.TypeAuthLogout || e.SpecVersion != "1.0" || e.DataContentType != "application/json" {
		t.Errorf("envelope = %+v, want type %s, spec 1.0, JSON", e, eventdata.TypeAuthLogout)
	}
	var data map[string]string
	if err := json.Unmarshal(e.Data, &data); err != nil || data["user"] != "ada" {
		t.Errorf("data = %s (%v), want user ada", e.Data, err)
	}
}

// A module that fixed the event id before publishing (an outbox) passes it,
// so a resend after a crash names the same event; without one the bus
// assigns it.
func TestPublishData_PassesAGivenIDAndLeavesItEmptyOtherwise(t *testing.T) {
	t.Parallel()
	bus := &recordingBus{}
	p := core.Publication{
		Source: "/daemon", Actor: "system:auth", ChartScope: "machine",
		Data: eventdata.AuthLogout{User: "ada"},
	}

	given := p
	given.ID = "evt_fixed"
	for _, pub := range []core.Publication{given, p} {
		if _, err := core.PublishData(t.Context(), bus, pub); err != nil {
			t.Fatalf("PublishData() error = %v", err)
		}
	}

	if got := []string{bus.got[0].ID, bus.got[1].ID}; got[0] != "evt_fixed" || got[1] != "" {
		t.Fatalf("event ids = %q, want [evt_fixed, empty]", got)
	}
}

func TestPublishData_RefusesMissingData(t *testing.T) {
	t.Parallel()

	_, err := core.PublishData(t.Context(), &recordingBus{}, core.Publication{Source: "/daemon"})

	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("PublishData() error = %v, want %v", err, core.ErrInvalid)
	}
}

// Daemon modules announce events with core.PublishData and a generated type.
// Building a core.Event or calling Events.Publish by hand would let a payload
// drift from the contract catalog, so only the events module, the core and the
// auth port adapter may.
func TestOnlyTheEventsModuleBuildsRawEvents(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	allowed := func(rel string) bool {
		rel = filepath.ToSlash(rel)
		return strings.HasPrefix(rel, "internal/core/") || strings.HasPrefix(rel, "internal/events/") ||
			rel == "internal/auth/module.go"
	}
	fset := token.NewFileSet()
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			if allowed(rel) {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CompositeLit:
					if sel, ok := x.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Event" && isIdent(sel.X, "core") {
						t.Errorf("%s: builds a core.Event by hand; use core.PublishData with a generated eventdata type", fset.Position(x.Pos()))
					}
				case *ast.CallExpr:
					if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Publish" {
						if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "Events" {
							t.Errorf("%s: calls Events.Publish by hand; use core.PublishData with a generated eventdata type", fset.Position(x.Pos()))
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}
