// SPDX-License-Identifier: MIT

package domain_test

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/control/domain"
)

func entropy(n int) *bytes.Reader {
	return bytes.NewReader(bytes.Repeat([]byte{0x5a, 0x13, 0xc7, 0x02}, n))
}

func TestNewSetupCode_IsThreeGroupsOfFourEasyToReadCharacters(t *testing.T) {
	t.Parallel()
	code, err := domain.NewSetupCode(entropy(16))
	if err != nil {
		t.Fatalf("NewSetupCode() error = %v", err)
	}

	if !regexp.MustCompile(`^[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}(-[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}){2}$`).MatchString(code.String()) {
		t.Fatalf("code = %q, want three groups of four from the easy-to-read alphabet", code.String())
	}
}

func TestNewSetupCode_FailsWhenEntropyRunsOut(t *testing.T) {
	t.Parallel()
	_, err := domain.NewSetupCode(bytes.NewReader(nil))
	if err == nil {
		t.Fatal("NewSetupCode() with no entropy: error = nil, want an error")
	}
}

func TestSetupCode_Matches(t *testing.T) {
	t.Parallel()
	code, err := domain.NewSetupCode(entropy(16))
	if err != nil {
		t.Fatalf("NewSetupCode() error = %v", err)
	}
	plain := strings.ReplaceAll(code.String(), "-", "")
	other := "A"
	if code.String()[0] == 'A' {
		other = "B"
	}
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"exact code", code.String(), true},
		{"lower case", strings.ToLower(code.String()), true},
		{"without dashes", plain, true},
		{"with spaces around and inside", "  " + plain[:4] + " " + plain[4:8] + " " + plain[8:] + " ", true},
		{"one character changed", other + code.String()[1:], false},
		{"too short", plain[:11], false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := code.Matches(tt.input); got != tt.want {
				t.Fatalf("Matches(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSetupCode_ZeroValueMatchesNothing(t *testing.T) {
	t.Parallel()
	var code domain.SetupCode
	if code.Matches("") {
		t.Fatal("zero code matched the empty input")
	}
	if code.String() != "" {
		t.Fatalf("zero code String() = %q, want empty", code.String())
	}
}

func TestCheckPassphrase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pass    string
		confirm string
		want    error
	}{
		{"eleven characters", "abcdefghijk", "abcdefghijk", domain.ErrPassphraseTooShort},
		{"twelve characters", "abcdefghijkl", "abcdefghijkl", nil},
		{"twelve characters, some outside ASCII", "ééééééééééé1", "ééééééééééé1", nil},
		{"eleven runes but more bytes", "éééééééééé1", "éééééééééé1", domain.ErrPassphraseTooShort},
		{"twelve spaces", strings.Repeat(" ", 12), strings.Repeat(" ", 12), domain.ErrPassphraseTooShort},
		{"at the length limit", strings.Repeat("a", domain.MaxPassphraseBytes), strings.Repeat("a", domain.MaxPassphraseBytes), nil},
		{"one byte over the limit", strings.Repeat("a", domain.MaxPassphraseBytes+1), strings.Repeat("a", domain.MaxPassphraseBytes+1), domain.ErrPassphraseTooLong},
		{"confirmation differs", "abcdefghijkl", "abcdefghijkm", domain.ErrPassphraseMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := domain.CheckPassphrase(tt.pass, tt.confirm); !errors.Is(err, tt.want) {
				t.Fatalf("CheckPassphrase() error = %v, want %v", err, tt.want)
			}
		})
	}
}
