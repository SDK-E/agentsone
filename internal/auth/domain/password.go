// SPDX-License-Identifier: MIT

package domain

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/SDK-E/agentsone/internal/core"
)

// Password length limits from the contract.
const (
	MinPasswordLen = 12
	MaxPasswordLen = 256
)

// Argon2Params are argon2id costs. They are stored in every hash, so they
// can be raised later without breaking old hashes.
type Argon2Params struct {
	Time    uint32
	Memory  uint32 // KiB
	Threads uint8
}

// DefaultArgon2 follows the RFC 9106 second recommended option.
var DefaultArgon2 = Argon2Params{Time: 3, Memory: 64 * 1024, Threads: 4}

const (
	saltLen = 16
	keyLen  = 32
)

// CheckPassword rejects passwords outside the contract's length limits.
func CheckPassword(pw string) error {
	if n := len(pw); n < MinPasswordLen || n > MaxPasswordLen {
		return fmt.Errorf("password must be %d to %d characters: %w", MinPasswordLen, MaxPasswordLen, core.ErrInvalid)
	}
	return nil
}

// HashPassword returns a PHC string: $argon2id$v=19$m=..,t=..,p=..$salt$key.
func HashPassword(rand io.Reader, p Argon2Params, pw string) (string, error) {
	if err := CheckPassword(pw); err != nil {
		return "", err
	}
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand, salt); err != nil {
		return "", fmt.Errorf("password salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, p.Time, p.Memory, p.Threads, keyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword reports whether pw matches a hash made by HashPassword.
// A malformed hash never matches.
func VerifyPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false
	}
	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil || p.Threads == 0 || p.Time == 0 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, p.Time, p.Memory, p.Threads, uint32(len(want))) // #nosec G115 -- len(want) is a decoded key, far below 2^32
	return subtle.ConstantTimeCompare(got, want) == 1
}
