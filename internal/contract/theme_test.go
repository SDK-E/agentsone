// SPDX-License-Identifier: MIT

package contract_test

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"testing"
)

func TestContrastRatio(t *testing.T) {
	cases := []struct {
		fg, bg string
		want   float64
	}{
		{"#000000", "#FFFFFF", 21},
		{"#FFFFFF", "#000000", 21},
		{"#777777", "#FFFFFF", 4.48},
		{"#2F5BD3", "#2F5BD3", 1},
	}
	for _, c := range cases {
		got, err := contrastRatio(c.fg, c.bg)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(got-c.want) > 0.01 {
			t.Errorf("contrast(%s, %s) = %.3f, want %.2f", c.fg, c.bg, got, c.want)
		}
	}
	if _, err := contrastRatio("#12345", "#FFFFFF"); err == nil {
		t.Error("short colour accepted")
	}
}

type theme struct {
	ContrastPairs []struct {
		FG       string  `json:"fg"`
		BG       string  `json:"bg"`
		MinRatio float64 `json:"minRatio"`
	} `json:"contrastPairs"`
	Modes map[string]struct {
		Color map[string]string `json:"color"`
	} `json:"modes"`
}

// TestThemeFixturesMeetTheirContrastPairs checks WCAG 2.2 contrast for every
// declared pair in every mode of every valid theme fixture.
func TestThemeFixturesMeetTheirContrastPairs(t *testing.T) {
	dir := requireDir(t, "fixtures/schemas/theme/valid")
	for file, raw := range readJSONFiles(t, dir) {
		var th theme
		if err := json.Unmarshal(raw, &th); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if len(th.ContrastPairs) == 0 || len(th.Modes) == 0 {
			t.Errorf("%s declares no contrast pairs or modes", filepath.Base(file))
		}
		for mode, tokens := range th.Modes {
			for _, p := range th.ContrastPairs {
				if err := checkPair(tokens.Color, p.FG, p.BG, p.MinRatio); err != nil {
					t.Errorf("%s %s mode: %v", file, mode, err)
				}
			}
		}
	}
}

func checkPair(colors map[string]string, fg, bg string, minRatio float64) error {
	fgHex, bgHex := colors[fg], colors[bg]
	if fgHex == "" || bgHex == "" {
		return fmt.Errorf("pair %s on %s names a missing colour role", fg, bg)
	}
	ratio, err := contrastRatio(fgHex, bgHex)
	if err != nil {
		return err
	}
	if ratio < minRatio {
		return fmt.Errorf("%s (%s) on %s (%s) has contrast %.2f, needs %.2f", fg, fgHex, bg, bgHex, ratio, minRatio)
	}
	return nil
}

// contrastRatio is the WCAG 2.2 contrast ratio of two #RRGGBB colours.
func contrastRatio(a, b string) (float64, error) {
	la, err := relativeLuminance(a)
	if err != nil {
		return 0, err
	}
	lb, err := relativeLuminance(b)
	if err != nil {
		return 0, err
	}
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05), nil
}

func relativeLuminance(hex string) (float64, error) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, fmt.Errorf("colour %q is not #RRGGBB", hex)
	}
	weights := [3]float64{0.2126, 0.7152, 0.0722}
	var l float64
	for i, w := range weights {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return 0, fmt.Errorf("colour %q: %w", hex, err)
		}
		l += w * linear(float64(v)/255)
	}
	return l, nil
}

func linear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}
