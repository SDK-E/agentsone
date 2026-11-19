// SPDX-License-Identifier: MIT

// Package auditchain canonicalises, hashes and verifies hash-chained audit records.
//
// A record is a JSON object. Its "hash" is the lowercase hex SHA-256 of the RFC 8785 (JCS)
// canonical form of the record without the "hash" member. Its "prevHash" is the previous
// record's hash, or Genesis for the first record. The daemon's audit module and the host's audit
// sink both use this package, so both sides agree byte for byte.
//
// Owned by work package 0 (frozen seam).
package auditchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gowebpki/jcs"
)

// Genesis is the prevHash of the first record in a chain: 64 zeros.
const Genesis = "0000000000000000000000000000000000000000000000000000000000000000"

// Field names used by the chain.
const (
	FieldHash     = "hash"
	FieldPrevHash = "prevHash"
)

// Canonicalize returns the RFC 8785 JSON Canonicalization Scheme form of raw.
func Canonicalize(raw []byte) ([]byte, error) {
	out, err := jcs.Transform(raw)
	if err != nil {
		return nil, fmt.Errorf("auditchain: canonicalize: %w", err)
	}
	return out, nil
}

func decodeObject(record []byte) (map[string]json.RawMessage, error) {
	// Validate and normalise first, so malformed JSON never reaches the hash.
	if _, err := Canonicalize(record); err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(record))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("auditchain: record is not a JSON object: %w", err)
	}
	if obj == nil {
		return nil, errors.New("auditchain: record is not a JSON object")
	}
	return obj, nil
}

func hashObject(obj map[string]json.RawMessage) (string, error) {
	body := make(map[string]json.RawMessage, len(obj))
	for k, v := range obj {
		if k != FieldHash {
			body[k] = v
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("auditchain: encode record: %w", err)
	}
	canon, err := Canonicalize(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}

// Hash returns the hash of record: SHA-256 over the JCS form of record without its "hash"
// member, as lowercase hex.
func Hash(record []byte) (string, error) {
	obj, err := decodeObject(record)
	if err != nil {
		return "", err
	}
	return hashObject(obj)
}

// Seal sets record's prevHash to prev, computes its hash, and returns the record in JCS form
// with both members set. Writers call it for every new record.
func Seal(record []byte, prev string) ([]byte, error) {
	obj, err := decodeObject(record)
	if err != nil {
		return nil, err
	}
	p, err := json.Marshal(prev)
	if err != nil {
		return nil, fmt.Errorf("auditchain: encode prevHash: %w", err)
	}
	obj[FieldPrevHash] = p
	h, err := hashObject(obj)
	if err != nil {
		return nil, err
	}
	hv, err := json.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("auditchain: encode hash: %w", err)
	}
	obj[FieldHash] = hv
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("auditchain: encode record: %w", err)
	}
	return Canonicalize(raw)
}

// BreakError reports where and why a chain fails verification.
type BreakError struct {
	// Index is the position of the first bad record.
	Index int
	// Reason says what is wrong with it.
	Reason string
}

func (e *BreakError) Error() string {
	return fmt.Sprintf("auditchain: record %d: %s", e.Index, e.Reason)
}

func stringField(obj map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := obj[name]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// Verify checks a chain in order: the first prevHash is Genesis, every record's hash matches
// its content, and every prevHash equals the hash before it. It returns a *BreakError for the
// first bad record, or nil when the whole chain holds.
func Verify(records []json.RawMessage) error {
	prev := Genesis
	for i, rec := range records {
		obj, err := decodeObject(rec)
		if err != nil {
			return &BreakError{Index: i, Reason: err.Error()}
		}
		got, ok := stringField(obj, FieldPrevHash)
		if !ok {
			return &BreakError{Index: i, Reason: "missing or non-string prevHash"}
		}
		if got != prev {
			if i == 0 {
				return &BreakError{Index: i, Reason: "first prevHash is not the genesis hash"}
			}
			return &BreakError{Index: i, Reason: "prevHash does not match the previous record's hash"}
		}
		stated, ok := stringField(obj, FieldHash)
		if !ok {
			return &BreakError{Index: i, Reason: "missing or non-string hash"}
		}
		want, err := hashObject(obj)
		if err != nil {
			return &BreakError{Index: i, Reason: err.Error()}
		}
		if stated != want {
			return &BreakError{Index: i, Reason: "hash does not match the record's content"}
		}
		prev = stated
	}
	return nil
}
