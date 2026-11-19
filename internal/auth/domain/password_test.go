// SPDX-License-Identifier: MIT

package domain_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// cheap keeps tests fast; production uses domain.DefaultArgon2.
var cheap = domain.Argon2Params{Time: 1, Memory: 64, Threads: 1}

const pw = "correct horse battery"

func TestHashPassword_VerifiesTheSamePasswordOnly(t *testing.T) {
	t.Parallel()
	h, err := domain.HashPassword(randomBytes(9), cheap, pw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=64,t=1,p=1$") || strings.Contains(h, pw) {
		t.Fatalf("hash %q", h)
	}
	if !domain.VerifyPassword(h, pw) {
		t.Fatal("same password rejected")
	}
	if domain.VerifyPassword(h, pw+"!") {
		t.Fatal("other password accepted")
	}
}

func TestHashPassword_RejectsShortAndLongPasswords(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"short", strings.Repeat("x", domain.MaxPasswordLen+1)} {
		if _, err := domain.HashPassword(randomBytes(1), cheap, p); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("len %d: err %v", len(p), err)
		}
	}
}

func TestHashPassword_FailsWhenRandomnessFails(t *testing.T) {
	t.Parallel()
	if _, err := domain.HashPassword(bytes.NewReader(nil), cheap, pw); err == nil {
		t.Fatal("no error")
	}
}

func TestVerifyPassword_RejectsMalformedHashes(t *testing.T) {
	t.Parallel()
	good, _ := domain.HashPassword(randomBytes(9), cheap, pw)
	parts := strings.Split(good, "$")
	for name, h := range map[string]string{
		"empty":         "",
		"bcrypt":        "$2y$10$abc",
		"wrong version": strings.Replace(good, "v=19", "v=16", 1),
		"bad params":    strings.Join([]string{"", parts[1], parts[2], "m=x", parts[4], parts[5]}, "$"),
		"zero threads":  strings.Join([]string{"", parts[1], parts[2], "m=64,t=1,p=0", parts[4], parts[5]}, "$"),
		"bad salt":      strings.Join([]string{"", parts[1], parts[2], parts[3], "!!", parts[5]}, "$"),
		"bad key":       strings.Join([]string{"", parts[1], parts[2], parts[3], parts[4], "!!"}, "$"),
		"empty key":     strings.Join([]string{"", parts[1], parts[2], parts[3], parts[4], ""}, "$"),
	} {
		if domain.VerifyPassword(h, pw) {
			t.Errorf("%s accepted", name)
		}
	}
}
