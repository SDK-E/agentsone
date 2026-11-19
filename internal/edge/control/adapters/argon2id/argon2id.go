// SPDX-License-Identifier: MIT

// Package argon2id hashes the control passphrase with Argon2id and writes the result as a PHC
// string: $argon2id$v=19$m=<KiB>,t=<passes>,p=<threads>$<salt>$<hash>, base64 without padding.
package argon2id

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params are the Argon2id costs.
type Params struct {
	// Memory in KiB.
	Memory  uint32
	Time    uint32
	Threads uint8
}

// DefaultParams follow the OWASP minimum for Argon2id: 19 MiB, two passes, one thread.
var DefaultParams = Params{Memory: 19 * 1024, Time: 2, Threads: 1}

// TestParams are cheap, for tests only.
var TestParams = Params{Memory: 64, Time: 1, Threads: 1}

// maxMemory bounds the memory a stored hash may ask for (256 MiB), so a hash written by hand
// cannot exhaust the container.
const maxMemory = 256 * 1024

const (
	saltBytes = 16
	keyBytes  = 32
)

var errMalformed = errors.New("argon2id: the stored hash is not a valid argon2id hash")

// Hasher hashes with Params.
type Hasher struct{ p Params }

// New returns a hasher that makes new hashes with p.
func New(p Params) Hasher { return Hasher{p: p} }

// Hash hashes pass with a fresh random salt.
func (h Hasher) Hash(pass string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2id: salt: %w", err)
	}
	key := argon2.IDKey([]byte(pass), salt, h.p.Time, h.p.Memory, h.p.Threads, keyBytes)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.p.Memory, h.p.Time, h.p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify tells whether pass matches hash, using the costs written in hash.
func (h Hasher) Verify(hash, pass string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false, errMalformed
	}
	var p Params
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil || n != 3 {
		return false, errMalformed
	}
	if p.Memory == 0 || p.Memory > maxMemory || p.Time == 0 || p.Threads == 0 {
		return false, errMalformed
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errMalformed
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errMalformed
	}
	// #nosec G115 -- len(want) is a decoded hash length, far below 2^32.
	got := argon2.IDKey([]byte(pass), salt, p.Time, p.Memory, p.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
