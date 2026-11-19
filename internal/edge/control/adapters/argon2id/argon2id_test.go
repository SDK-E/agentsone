// SPDX-License-Identifier: MIT

package argon2id_test

import (
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/control/adapters/argon2id"
)

func TestHash_IsAPHCStringThatVerifiesOnlyTheSamePassphrase(t *testing.T) {
	t.Parallel()
	h := argon2id.New(argon2id.TestParams)

	hash, err := h.Hash("correct horse battery")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	if !strings.HasPrefix(hash, "$argon2id$v=19$m=") {
		t.Fatalf("hash = %q, want a PHC argon2id string", hash)
	}
	if strings.Contains(hash, "correct horse") {
		t.Fatal("the hash holds the passphrase")
	}
	if ok, err := h.Verify(hash, "correct horse battery"); err != nil || !ok {
		t.Fatalf("Verify(right) = %v, %v", ok, err)
	}
	if ok, err := h.Verify(hash, "correct horse batterY"); err != nil || ok {
		t.Fatalf("Verify(wrong) = %v, %v", ok, err)
	}
}

func TestHash_SaltsEveryHash(t *testing.T) {
	t.Parallel()
	h := argon2id.New(argon2id.TestParams)
	a, _ := h.Hash("same passphrase here")
	b, _ := h.Hash("same passphrase here")
	if a == b {
		t.Fatal("two hashes of one passphrase are equal: no salt")
	}
}

func TestVerify_UsesTheParametersStoredInTheHash(t *testing.T) {
	t.Parallel()
	hash, _ := argon2id.New(argon2id.TestParams).Hash("correct horse battery")

	ok, err := argon2id.New(argon2id.Params{Memory: 64, Time: 3, Threads: 1}).Verify(hash, "correct horse battery")

	if err != nil || !ok {
		t.Fatalf("Verify() with other default params = %v, %v", ok, err)
	}
}

func TestVerify_RefusesAMalformedHash(t *testing.T) {
	t.Parallel()
	h := argon2id.New(argon2id.TestParams)
	for _, bad := range []string{
		"",
		"plain",
		"$argon2i$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$aGFzaA",
		"$argon2id$v=18$m=8,t=1,p=1$c2FsdHNhbHQ$aGFzaA",
		"$argon2id$v=19$m=x,t=1,p=1$c2FsdHNhbHQ$aGFzaA",
		"$argon2id$v=19$m=8,t=1,p=1$!!!$aGFzaA",
		"$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$!!!",
		"$argon2id$v=19$m=8,t=0,p=1$c2FsdHNhbHQ$aGFzaA",
		"$argon2id$v=19$m=99999999,t=1,p=1$c2FsdHNhbHQ$aGFzaA",
	} {
		if ok, err := h.Verify(bad, "x"); err == nil || ok {
			t.Errorf("Verify(%q) = %v, %v; want an error", bad, ok, err)
		}
	}
}

func TestDefaultParams_FollowTheOWASPMinimum(t *testing.T) {
	t.Parallel()
	p := argon2id.DefaultParams
	if p.Memory < 19*1024 || p.Time < 2 || p.Threads < 1 {
		t.Fatalf("DefaultParams = %+v, below m=19 MiB, t=2, p=1", p)
	}
}
