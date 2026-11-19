// SPDX-License-Identifier: MIT

// Package domain holds the rules of the control page: the one-time setup code, the passphrase
// rules, sessions, the sign-in limiter and the money and share inputs of the budget form. It is
// pure: time comes in as arguments and randomness through an io.Reader.
package domain

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Errors of the passphrase rules.
var (
	ErrPassphraseTooShort = errors.New("the passphrase needs at least 12 characters")
	ErrPassphraseTooLong  = errors.New("the passphrase is too long")
	ErrPassphraseMismatch = errors.New("the two passphrases are not the same")
)

// MinPassphraseRunes is the shortest passphrase, in characters. Spaces at the ends do not count.
const MinPassphraseRunes = 12

// MaxPassphraseBytes bounds a passphrase, so hashing stays cheap to refuse.
const MaxPassphraseBytes = 1024

// CheckPassphrase applies the passphrase rules to a new passphrase and its confirmation. Any
// character is allowed; length is what counts.
func CheckPassphrase(pass, confirm string) error {
	if len(pass) > MaxPassphraseBytes {
		return ErrPassphraseTooLong
	}
	if utf8.RuneCountInString(strings.TrimFunc(pass, unicode.IsSpace)) < MinPassphraseRunes {
		return ErrPassphraseTooShort
	}
	if subtle.ConstantTimeCompare([]byte(pass), []byte(confirm)) != 1 {
		return ErrPassphraseMismatch
	}
	return nil
}

// codeAlphabet has no 0, O, 1, I or L, so a code read from a log is hard to mistype.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

const (
	codeGroups    = 3
	codeGroupSize = 4
	codeLength    = codeGroups * codeGroupSize
)

// SetupCode is the one-time code the control service prints to its log while no passphrase is
// set. Whoever can read that log (the person who runs the stack) can set the passphrase. 12
// characters from a 31-character alphabet are about 59 bits.
type SetupCode struct{ code string }

// NewSetupCode draws a code from entropy without modulo bias.
func NewSetupCode(entropy io.Reader) (SetupCode, error) {
	out := make([]byte, 0, codeLength)
	buf := make([]byte, 1)
	// The largest multiple of the alphabet size that fits in a byte: bytes at or above it are
	// skipped so every character is equally likely.
	limit := byte(256 - 256%len(codeAlphabet))
	for len(out) < codeLength {
		if _, err := io.ReadFull(entropy, buf); err != nil {
			return SetupCode{}, fmt.Errorf("draw setup code: %w", err)
		}
		if buf[0] >= limit {
			continue
		}
		out = append(out, codeAlphabet[int(buf[0])%len(codeAlphabet)])
	}
	return SetupCode{code: string(out)}, nil
}

// String is the code as printed: three groups of four, joined by dashes.
func (c SetupCode) String() string {
	if c.code == "" {
		return ""
	}
	parts := make([]string, 0, codeGroups)
	for i := 0; i < codeLength; i += codeGroupSize {
		parts = append(parts, c.code[i:i+codeGroupSize])
	}
	return strings.Join(parts, "-")
}

// Matches tells whether input is the code. Case, spaces and dashes do not matter. The zero code
// matches nothing.
func (c SetupCode) Matches(input string) bool {
	if c.code == "" {
		return false
	}
	norm := strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToUpper(r)
	}, input)
	return subtle.ConstantTimeCompare([]byte(norm), []byte(c.code)) == 1
}
