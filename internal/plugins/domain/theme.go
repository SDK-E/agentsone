// SPDX-License-Identifier: MIT

package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxThemeBytes is the largest theme document ParseTheme reads.
const MaxThemeBytes = 256 * 1024

// Mode is which token set of a theme is drawn.
type Mode string

// Modes a human can choose. System follows the operating system.
const (
	ModeSystem Mode = "system"
	ModeLight  Mode = "light"
	ModeDark   Mode = "dark"
)

// ParseMode checks that s is system, light or dark.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case ModeSystem, ModeLight, ModeDark:
		return m, nil
	}
	return "", themeErr("mode %q is not system, light or dark", s)
}

// Theme is a validated theme document (contract/schemas/theme.schema.json).
type Theme struct {
	Name        string
	Version     string
	Author      string
	Description string
	// Document is the theme as stored and served: valid, compact JSON.
	Document json.RawMessage
}

// InstalledTheme is a theme the daemon knows.
type InstalledTheme struct {
	ID          string // thm_<ULID>
	Name        string
	Version     string
	Builtin     bool   // true for the default Agents One Theme
	PluginID    string // plg_<ULID> of the plugin that carries it; empty when imported by hand
	InstalledAt time.Time
	Document    json.RawMessage
}

// CheckDeletable refuses the built-in theme.
func (t InstalledTheme) CheckDeletable() error {
	if t.Builtin {
		return fmt.Errorf("theme %q: %w", t.Name, ErrBuiltin)
	}
	return nil
}

// Selection is one human's theme and mode.
type Selection struct {
	ThemeID string
	Mode    Mode
}

// DefaultSelection is what a human sees before choosing: the built-in theme,
// following the system.
func DefaultSelection(builtinID string) Selection {
	return Selection{ThemeID: builtinID, Mode: ModeSystem}
}

// ThemeRepository stores themes and each human's selection.
type ThemeRepository interface {
	// Insert adds t. A theme with the same name is ErrAlreadyInstalled.
	Insert(ctx context.Context, t InstalledTheme) error
	// Update replaces the version and document of the theme with t.ID.
	Update(ctx context.Context, t InstalledTheme) error
	// Get returns the theme, or ErrNotFound.
	Get(ctx context.Context, id string) (InstalledTheme, error)
	// FindByName returns the theme with this name.
	FindByName(ctx context.Context, name string) (InstalledTheme, bool, error)
	// Builtin returns the built-in theme, and false before it is seeded.
	Builtin(ctx context.Context) (InstalledTheme, bool, error)
	// ByPlugin returns the themes a plugin carries.
	ByPlugin(ctx context.Context, pluginID string) ([]InstalledTheme, error)
	// List returns up to limit themes with an id after the given id, oldest first.
	List(ctx context.Context, afterID string, limit int) ([]InstalledTheme, error)
	// Delete removes the theme: ErrNotFound when missing, ErrInUse when any
	// human has it selected.
	Delete(ctx context.Context, id string) error
	// Selection returns the human's choice, and false when none was made.
	Selection(ctx context.Context, human string) (Selection, bool, error)
	// SetSelection stores the choice: ErrNotFound when the theme is missing.
	SetSelection(ctx context.Context, human string, s Selection) error
	// ReassignSelections moves every human who selected from to the theme to.
	ReassignSelections(ctx context.Context, from, to string) error
}

func themeErr(format string, a ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, a...), ErrInvalidTheme)
}

// colorRoles are the roles of the schema's ColorRoles, all required.
var colorRoles = []string{
	"background", "surface", "surfaceRaised", "surfaceSunken", "border", "borderStrong",
	"text", "textMuted", "accent", "accentHover", "onAccent", "accentSubtle", "onAccentSubtle",
	"alive", "visitor", "visitorSubtle", "onVisitorSubtle", "success", "warning", "danger",
	"onDanger", "info", "focus",
}

var (
	colorRe    = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	lengthRe   = regexp.MustCompile(`^(0|[0-9]+(\.[0-9]+)?(px|rem))$`)
	fontRe     = regexp.MustCompile(`^[A-Za-z0-9 ,"'-]+$`)
	shadowRe   = regexp.MustCompile(`^[A-Za-z0-9 #.,()%/-]+$`)
	easingRe   = regexp.MustCompile(`^(linear|ease|ease-in|ease-out|ease-in-out|cubic-bezier\(-?[0-9.]+, ?-?[0-9.]+, ?-?[0-9.]+, ?-?[0-9.]+\))$`)
	scaleSteps = []string{"xs", "sm", "md", "lg", "xl", "xxl"}
	weightList = map[int]bool{100: true, 200: true, 300: true, 400: true, 500: true, 600: true, 700: true, 800: true, 900: true}
)

type themeDoc struct {
	Schema        string            `json:"$schema"`
	Name          string            `json:"name"`
	Version       string            `json:"version"`
	Author        *themeAuthor      `json:"author"`
	Description   *string           `json:"description"`
	Modes         *themeModes       `json:"modes"`
	Typography    *themeTypography  `json:"typography"`
	Radius        map[string]string `json:"radius"`
	Spacing       map[string]string `json:"spacing"`
	Motion        *themeMotion      `json:"motion"`
	ContrastPairs []themePair       `json:"contrastPairs"`
}

type themeAuthor struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type themeModes struct {
	Light *themeModeTokens `json:"light"`
	Dark  *themeModeTokens `json:"dark"`
}

type themeModeTokens struct {
	Color  map[string]string `json:"color"`
	Shadow map[string]string `json:"shadow"`
}

type themeTypography struct {
	Sans    string                    `json:"sans"`
	Mono    string                    `json:"mono"`
	Scale   map[string]themeTextStyle `json:"scale"`
	Weights map[string]int            `json:"weights"`
}

type themeTextStyle struct {
	Size       string   `json:"size"`
	LineHeight *float64 `json:"lineHeight"`
}

type themeMotion struct {
	FastMs   *int   `json:"fastMs"`
	NormalMs *int   `json:"normalMs"`
	SlowMs   *int   `json:"slowMs"`
	Easing   string `json:"easing"`
}

type themePair struct {
	Fg       string   `json:"fg"`
	Bg       string   `json:"bg"`
	MinRatio *float64 `json:"minRatio"`
}

// ParseTheme reads a theme document, checks every rule of the theme schema
// and checks that each declared contrast pair meets its ratio in both modes.
// Every refusal wraps ErrInvalidTheme, except size (ErrTooLarge).
func ParseTheme(raw []byte) (Theme, error) {
	if len(raw) > MaxThemeBytes {
		return Theme{}, fmt.Errorf("theme is %d bytes, the limit is %d: %w", len(raw), MaxThemeBytes, ErrTooLarge)
	}
	var d themeDoc
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return Theme{}, themeErr("not a theme document (%v)", err)
	}
	if dec.More() {
		return Theme{}, themeErr("data after the theme document")
	}
	if err := d.validate(); err != nil {
		return Theme{}, err
	}
	if err := d.checkContrast(); err != nil {
		return Theme{}, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return Theme{}, themeErr("not a theme document (%v)", err)
	}
	t := Theme{Name: d.Name, Version: d.Version, Author: d.Author.Name, Document: compact.Bytes()}
	if d.Description != nil {
		t.Description = *d.Description
	}
	return t, nil
}

func (d themeDoc) validate() error {
	if n := utf8.RuneCountInString(d.Name); n < 1 || n > 64 || strings.ContainsAny(d.Name, "\r\n") {
		return themeErr("name must be 1 to 64 characters on one line")
	}
	if !IsSemver(d.Version) {
		return themeErr("version %q is not a semantic version", d.Version)
	}
	if d.Schema != "" && !validURL(d.Schema) {
		return themeErr("$schema is not a URL")
	}
	if d.Author == nil || d.Author.Name == "" || utf8.RuneCountInString(d.Author.Name) > 128 {
		return themeErr("author.name must be 1 to 128 characters")
	}
	if d.Author.URL != "" && (!validURL(d.Author.URL) || len(d.Author.URL) > 512) {
		return themeErr("author.url is not a URL")
	}
	if d.Description != nil && !ValidOneLine(*d.Description) {
		return themeErr("description must be 1 to 255 characters on one line")
	}
	if d.Modes == nil || d.Modes.Light == nil || d.Modes.Dark == nil {
		return themeErr("modes must carry both light and dark")
	}
	if err := d.Modes.Light.validate("light"); err != nil {
		return err
	}
	if err := d.Modes.Dark.validate("dark"); err != nil {
		return err
	}
	if err := d.Typography.validate(); err != nil {
		return err
	}
	if err := validateLengths("radius", d.Radius, "sm", "md", "lg", "full"); err != nil {
		return err
	}
	if err := validateLengths("spacing", d.Spacing, "xs", "sm", "md", "lg", "xl"); err != nil {
		return err
	}
	if err := d.Motion.validate(); err != nil {
		return err
	}
	return d.validatePairs()
}

func (m *themeModeTokens) validate(mode string) error {
	if m == nil || len(m.Color) != len(colorRoles) {
		return themeErr("modes.%s.color must define exactly the %d color roles", mode, len(colorRoles))
	}
	for _, role := range colorRoles {
		if c, ok := m.Color[role]; !ok || !colorRe.MatchString(c) {
			return themeErr("modes.%s.color.%s must be #RRGGBB", mode, role)
		}
	}
	if len(m.Shadow) != 3 {
		return themeErr("modes.%s.shadow must define sm, md and lg", mode)
	}
	for _, k := range []string{"sm", "md", "lg"} {
		s, ok := m.Shadow[k]
		if !ok || len(s) == 0 || len(s) > 200 || !shadowRe.MatchString(s) {
			return themeErr("modes.%s.shadow.%s is not a plain CSS shadow", mode, k)
		}
	}
	return nil
}

func (t *themeTypography) validate() error {
	if t == nil {
		return themeErr("typography is required")
	}
	for name, stack := range map[string]string{"sans": t.Sans, "mono": t.Mono} {
		if stack == "" || len(stack) > 256 || !fontRe.MatchString(stack) {
			return themeErr("typography.%s is not a plain font-family list", name)
		}
	}
	if len(t.Scale) != len(scaleSteps) {
		return themeErr("typography.scale must define %s", strings.Join(scaleSteps, ", "))
	}
	for _, step := range scaleSteps {
		s, ok := t.Scale[step]
		if !ok || !lengthRe.MatchString(s.Size) || s.LineHeight == nil || *s.LineHeight < 1 || *s.LineHeight > 3 {
			return themeErr("typography.scale.%s needs a size and a lineHeight from 1 to 3", step)
		}
	}
	if len(t.Weights) != 3 {
		return themeErr("typography.weights must define regular, medium and bold")
	}
	for _, k := range []string{"regular", "medium", "bold"} {
		if w, ok := t.Weights[k]; !ok || !weightList[w] {
			return themeErr("typography.weights.%s must be 100, 200, ... 900", k)
		}
	}
	return nil
}

func validateLengths(what string, got map[string]string, keys ...string) error {
	if len(got) != len(keys) {
		return themeErr("%s must define %s", what, strings.Join(keys, ", "))
	}
	for _, k := range keys {
		if v, ok := got[k]; !ok || !lengthRe.MatchString(v) {
			return themeErr("%s.%s must be 0 or a length in px or rem", what, k)
		}
	}
	return nil
}

func (m *themeMotion) validate() error {
	if m == nil {
		return themeErr("motion is required")
	}
	for name, v := range map[string]*int{"fastMs": m.FastMs, "normalMs": m.NormalMs, "slowMs": m.SlowMs} {
		if v == nil || *v < 0 || *v > 2000 {
			return themeErr("motion.%s must be 0 to 2000", name)
		}
	}
	if !easingRe.MatchString(m.Easing) {
		return themeErr("motion.easing must be a CSS keyword or cubic-bezier()")
	}
	return nil
}

func (d themeDoc) validatePairs() error {
	if n := len(d.ContrastPairs); n < 1 || n > 64 {
		return themeErr("contrastPairs must list 1 to 64 pairs")
	}
	known := map[string]bool{}
	for _, r := range colorRoles {
		known[r] = true
	}
	for i, p := range d.ContrastPairs {
		if !known[p.Fg] || !known[p.Bg] {
			return themeErr("contrastPairs[%d]: fg and bg must be color roles", i)
		}
		if p.MinRatio == nil || (*p.MinRatio != 3 && *p.MinRatio != 4.5 && *p.MinRatio != 7) {
			return themeErr("contrastPairs[%d]: minRatio must be 3, 4.5 or 7", i)
		}
	}
	return nil
}

// checkContrast computes every declared pair in both modes.
func (d themeDoc) checkContrast() error {
	for _, mode := range []struct {
		name   string
		tokens *themeModeTokens
	}{{"light", d.Modes.Light}, {"dark", d.Modes.Dark}} {
		for _, p := range d.ContrastPairs {
			ratio, err := ContrastRatio(mode.tokens.Color[p.Fg], mode.tokens.Color[p.Bg])
			if err != nil {
				return err
			}
			if ratio < *p.MinRatio {
				return themeErr("%s mode: %s on %s is %.2f:1, needs %s:1",
					mode.name, p.Fg, p.Bg, ratio, strconv.FormatFloat(*p.MinRatio, 'f', -1, 64))
			}
		}
	}
	return nil
}

// ContrastRatio is the WCAG 2.x contrast ratio of two #RRGGBB colors, from 1
// to 21.
func ContrastRatio(fg, bg string) (float64, error) {
	lf, err := luminance(fg)
	if err != nil {
		return 0, err
	}
	lb, err := luminance(bg)
	if err != nil {
		return 0, err
	}
	hi, lo := math.Max(lf, lb), math.Min(lf, lb)
	return (hi + 0.05) / (lo + 0.05), nil
}

func luminance(hex string) (float64, error) {
	if !colorRe.MatchString(hex) {
		return 0, themeErr("color %q is not #RRGGBB", hex)
	}
	channel := func(s string) float64 {
		v, _ := strconv.ParseUint(s, 16, 8) //nolint:errcheck // colorRe above guarantees two hex digits
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(hex[1:3]) + 0.7152*channel(hex[3:5]) + 0.0722*channel(hex[5:7]), nil
}
