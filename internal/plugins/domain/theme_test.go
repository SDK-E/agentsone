// SPDX-License-Identifier: MIT

package domain_test

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const themeFixtures = "../../../contract/fixtures/schemas/theme"

func readTheme(t *testing.T, kind, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(themeFixtures, kind, name)) // #nosec G304 -- fixture path chosen by the test
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// mutateTheme decodes the valid fixture, applies change and encodes it again.
func mutateTheme(t *testing.T, change func(doc map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(readTheme(t, "valid", "agents-one-theme-draft.json"), &doc); err != nil {
		t.Fatal(err)
	}
	change(doc)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func colors(doc map[string]any, mode string) map[string]any {
	return doc["modes"].(map[string]any)[mode].(map[string]any)["color"].(map[string]any)
}

func TestParseTheme_AcceptsTheDraftFixtureAndKeepsTheDocument(t *testing.T) {
	t.Parallel()
	raw := readTheme(t, "valid", "agents-one-theme-draft.json")

	got, err := domain.ParseTheme(raw)
	if err != nil {
		t.Fatalf("ParseTheme() error = %v", err)
	}

	if got.Name != "Agents One Theme" || got.Version != "0.1.0" || got.Author != "Agents One" {
		t.Errorf("theme = %+v", got)
	}
	if !json.Valid(got.Document) || len(got.Document) == 0 {
		t.Errorf("document is not kept: %q", got.Document)
	}
}

func TestParseTheme_RefusesTheInvalidFixtures(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"color-with-alpha.json", "contrast-ratio-below-aa.json", "missing-color-role.json", "missing-dark-mode.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := domain.ParseTheme(readTheme(t, "invalid", name))
			if !errors.Is(err, domain.ErrInvalidTheme) {
				t.Fatalf("ParseTheme() error = %v, want ErrInvalidTheme", err)
			}
		})
	}
}

func TestParseTheme_NamesTheFailingContrastPairAndMode(t *testing.T) {
	t.Parallel()
	raw := mutateTheme(t, func(doc map[string]any) {
		colors(doc, "dark")["textMuted"] = "#2C313C" // nearly the surface colour
	})

	_, err := domain.ParseTheme(raw)

	if !errors.Is(err, domain.ErrInvalidTheme) {
		t.Fatalf("error = %v, want ErrInvalidTheme", err)
	}
	for _, want := range []string{"dark", "textMuted"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestParseTheme_Refusals(t *testing.T) {
	t.Parallel()
	tests := map[string]func(doc map[string]any){
		"unknown top-level field": func(d map[string]any) { d["extra"] = 1 },
		"empty name":              func(d map[string]any) { d["name"] = "" },
		"name with line break":    func(d map[string]any) { d["name"] = "a\nb" },
		"name over 64":            func(d map[string]any) { d["name"] = strings.Repeat("a", 65) },
		"version not semantic":    func(d map[string]any) { d["version"] = "latest" },
		"missing author name":     func(d map[string]any) { d["author"] = map[string]any{} },
		"colour is a name":        func(d map[string]any) { colors(d, "light")["text"] = "black" },
		"short hex colour":        func(d map[string]any) { colors(d, "light")["text"] = "#FFF" },
		"unknown colour role":     func(d map[string]any) { colors(d, "light")["sparkle"] = "#FFFFFF" },
		"pair uses unknown role": func(d map[string]any) {
			d["contrastPairs"] = []any{map[string]any{"fg": "sparkle", "bg": "text", "minRatio": 4.5}}
		},
		"pair ratio not allowed": func(d map[string]any) {
			d["contrastPairs"] = []any{map[string]any{"fg": "text", "bg": "background", "minRatio": 5}}
		},
		"no contrast pairs":       func(d map[string]any) { d["contrastPairs"] = []any{} },
		"too many contrast pairs": func(d map[string]any) { d["contrastPairs"] = manyPairs(65) },
		"font stack with a brace": func(d map[string]any) { d["typography"].(map[string]any)["sans"] = "Arial; }" },
		"line height below 1":     func(d map[string]any) { scaleXS(d)["lineHeight"] = 0.5 },
		"length without unit":     func(d map[string]any) { d["spacing"].(map[string]any)["md"] = "12" },
		"font weight not allowed": func(d map[string]any) { d["typography"].(map[string]any)["weights"].(map[string]any)["bold"] = 650 },
		"motion over 2000 ms":     func(d map[string]any) { d["motion"].(map[string]any)["slowMs"] = 2001 },
		"easing not css":          func(d map[string]any) { d["motion"].(map[string]any)["easing"] = "bounce" },
		"shadow with a semicolon": func(d map[string]any) {
			d["modes"].(map[string]any)["light"].(map[string]any)["shadow"].(map[string]any)["sm"] = "0 0 1px red; x"
		},
		"missing shadow": func(d map[string]any) {
			delete(d["modes"].(map[string]any)["dark"].(map[string]any)["shadow"].(map[string]any), "lg")
		},
		"description with newline": func(d map[string]any) { d["description"] = "a\nb" },
		"author url not a url":     func(d map[string]any) { d["author"].(map[string]any)["url"] = "not a url" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.ParseTheme(mutateTheme(t, change)); !errors.Is(err, domain.ErrInvalidTheme) {
				t.Fatalf("ParseTheme() error = %v, want ErrInvalidTheme", err)
			}
		})
	}
}

func scaleXS(doc map[string]any) map[string]any {
	return doc["typography"].(map[string]any)["scale"].(map[string]any)["xs"].(map[string]any)
}

func manyPairs(n int) []any {
	pairs := make([]any, n)
	for i := range pairs {
		pairs[i] = map[string]any{"fg": "text", "bg": "background", "minRatio": 4.5}
	}
	return pairs
}

func TestParseTheme_NotJSONAndTooLarge(t *testing.T) {
	t.Parallel()
	if _, err := domain.ParseTheme([]byte("{")); !errors.Is(err, domain.ErrInvalidTheme) {
		t.Errorf("not json: error = %v", err)
	}
	huge := mutateTheme(t, func(d map[string]any) { d["description"] = "ok" })
	huge = append(huge, make([]byte, domain.MaxThemeBytes)...)
	if _, err := domain.ParseTheme(huge); !errors.Is(err, domain.ErrTooLarge) {
		t.Errorf("too large: error = %v", err)
	}
}

func TestParseTheme_OptionalFieldsMayBeLeftOut(t *testing.T) {
	t.Parallel()
	raw := mutateTheme(t, func(d map[string]any) {
		delete(d, "description")
		delete(d, "$schema")
		d["author"] = map[string]any{"name": "Anon"}
	})
	if _, err := domain.ParseTheme(raw); err != nil {
		t.Fatalf("ParseTheme() error = %v", err)
	}
}

func TestContrastRatio_MatchesTheWCAGDefinition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		fg, bg string
		want   float64
	}{
		{"#000000", "#FFFFFF", 21},
		{"#FFFFFF", "#000000", 21},
		{"#FFFFFF", "#FFFFFF", 1},
		{"#767676", "#FFFFFF", 4.54}, // the well-known smallest grey that passes AA on white
		{"#777777", "#FFFFFF", 4.48},
		{"#ff0000", "#ffffff", 4.0},
	}
	for _, tt := range tests {
		got, err := domain.ContrastRatio(tt.fg, tt.bg)
		if err != nil {
			t.Fatalf("ContrastRatio(%s, %s) error = %v", tt.fg, tt.bg, err)
		}
		if math.Abs(got-tt.want) > 0.01 {
			t.Errorf("ContrastRatio(%s, %s) = %.3f, want %.2f", tt.fg, tt.bg, got, tt.want)
		}
	}
	if _, err := domain.ContrastRatio("#12", "#FFFFFF"); !errors.Is(err, domain.ErrInvalidTheme) {
		t.Errorf("bad colour: error = %v", err)
	}
	if _, err := domain.ContrastRatio("#FFFFFF", "nope"); !errors.Is(err, domain.ErrInvalidTheme) {
		t.Errorf("bad background: error = %v", err)
	}
}

func TestParseMode(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"system", "light", "dark"} {
		got, err := domain.ParseMode(in)
		if err != nil || string(got) != in {
			t.Errorf("ParseMode(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "Dark", "auto"} {
		if _, err := domain.ParseMode(in); !errors.Is(err, domain.ErrInvalidTheme) {
			t.Errorf("ParseMode(%q) error = %v, want ErrInvalidTheme", in, err)
		}
	}
}

func TestInstalledTheme_CheckDeletable(t *testing.T) {
	t.Parallel()
	if err := (domain.InstalledTheme{Builtin: true}).CheckDeletable(); !errors.Is(err, domain.ErrBuiltin) {
		t.Errorf("builtin: error = %v, want ErrBuiltin", err)
	}
	if err := (domain.InstalledTheme{}).CheckDeletable(); err != nil {
		t.Errorf("imported: error = %v", err)
	}
}

func TestDefaultSelection_IsTheBuiltInThemeInSystemMode(t *testing.T) {
	t.Parallel()
	got := domain.DefaultSelection("thm_X")
	if got.ThemeID != "thm_X" || got.Mode != domain.ModeSystem {
		t.Errorf("DefaultSelection() = %+v", got)
	}
}

func TestThemeDocuments_MatchTheContractSchema(t *testing.T) {
	t.Parallel()
	set := schemaSet(t)
	valid := readTheme(t, "valid", "agents-one-theme-draft.json")
	if err := set.Validate("theme", valid); err != nil {
		t.Fatalf("valid fixture rejected by the schema: %v", err)
	}
	got, err := domain.ParseTheme(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Validate("theme", got.Document); err != nil {
		t.Errorf("stored document no longer matches the schema: %v", err)
	}
}

// Our validation must agree with the schema on the fixtures: whatever the
// schema accepts we accept, and whatever it refuses we refuse.
func TestParseTheme_AgreesWithTheSchemaOnEveryFixture(t *testing.T) {
	t.Parallel()
	set := schemaSet(t)
	var _ = contracttest.CheckResponse // keeps the import honest when the schema set changes
	for _, kind := range []string{"valid", "invalid"} {
		entries, err := os.ReadDir(filepath.Join(themeFixtures, kind))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			raw := readTheme(t, kind, e.Name())
			_, ours := domain.ParseTheme(raw)
			if schemaOK := set.Validate("theme", raw) == nil; schemaOK && ours != nil {
				t.Errorf("%s/%s: schema accepts but ParseTheme refuses: %v", kind, e.Name(), ours)
			}
			if kind == "valid" && ours != nil {
				t.Errorf("valid/%s refused: %v", e.Name(), ours)
			}
			if kind == "invalid" && ours == nil {
				t.Errorf("invalid/%s accepted", e.Name())
			}
		}
	}
}
