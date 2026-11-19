// SPDX-License-Identifier: MIT

package auditchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestCanonicalizeVectors(t *testing.T) {
	cases := []struct{ in, want string }{
		// RFC 8785 section 3.2.2 / appendix B number serialisation.
		{`[1e30, 4.50, 0.002, 1E-27, -0, 333333333.33333329]`, `[1e+30,4.5,0.002,1e-27,0,333333333.3333333]`},
		{`{"numbers":[333333333.33333329,1E30,4.50,2e-3,0.000000000000000000000000001],"string":"€$\u000F\u000aA'B\"\\\\\"\/","literals":[null,true,false]}`,
			`{"literals":[null,true,false],"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27],"string":"€$\u000f\nA'B\"\\\\\"/"}`},
		// Members sort by UTF-16 code units.
		{`{"€":"Euro Sign","\r":"Carriage Return","דּ":"Hebrew Letter Dalet With Dagesh","1":"One","😀":"Emoji: Grinning Face","\u0080":"Control","ö":"Latin Small Letter O With Diaeresis"}`,
			`{"\r":"Carriage Return","1":"One","` + "\u0080" + `":"Control","ö":"Latin Small Letter O With Diaeresis","€":"Euro Sign","😀":"Emoji: Grinning Face","` + string(rune(0xFB33)) + `":"Hebrew Letter Dalet With Dagesh"}`},
		{` { "b" : 2 , "a" : { "d" : [ 1 , 2 ] , "c" : "x" } } `, `{"a":{"c":"x","d":[1,2]},"b":2}`},
	}
	for _, c := range cases {
		got, err := Canonicalize([]byte(c.in))
		if err != nil {
			t.Fatalf("Canonicalize(%s): %v", c.in, err)
		}
		if string(got) != c.want {
			t.Errorf("Canonicalize(%s)\n got  %s\n want %s", c.in, got, c.want)
		}
	}
	if _, err := Canonicalize([]byte(`{"a":`)); err == nil {
		t.Error("malformed JSON accepted")
	}
}

func TestHashIgnoresHashMemberAndOrder(t *testing.T) {
	a, err := Hash([]byte(`{"seq":1,"actor":"founder","hash":"whatever"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Hash([]byte(`{"actor":"founder","seq":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("hash differs: %s vs %s", a, b)
	}
	sum := sha256.Sum256([]byte(`{"actor":"founder","seq":1}`))
	if want := hex.EncodeToString(sum[:]); a != want {
		t.Errorf("Hash = %s, want %s", a, want)
	}
	if _, err := Hash([]byte(`[1,2]`)); err == nil {
		t.Error("array accepted as record")
	}
}

func chain(t *testing.T, n int) []json.RawMessage {
	t.Helper()
	prev := Genesis
	out := make([]json.RawMessage, 0, n)
	for i := range n {
		rec, err := json.Marshal(map[string]any{
			"seq": i, "time": "2026-11-19T20:00:00Z", "actor": "founder", "action": "agent.hire",
			"detail": map[string]any{"name": "scout", "amount": 1.5},
		})
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := Seal(rec, prev)
		if err != nil {
			t.Fatal(err)
		}
		var full map[string]any
		if err := json.Unmarshal(sealed, &full); err != nil {
			t.Fatal(err)
		}
		prev, _ = full[FieldHash].(string)
		out = append(out, sealed)
	}
	return out
}

func TestSealVerify(t *testing.T) {
	c := chain(t, 3)
	if err := Verify(c); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := Verify(nil); err != nil {
		t.Errorf("empty chain: %v", err)
	}
}

func expectBreak(t *testing.T, c []json.RawMessage, index int) {
	t.Helper()
	err := Verify(c)
	var be *BreakError
	if !errors.As(err, &be) {
		t.Fatalf("Verify = %v, want *BreakError", err)
	}
	if be.Index != index {
		t.Errorf("break at %d (%s), want %d", be.Index, be.Reason, index)
	}
}

func TestTamperChangedField(t *testing.T) {
	c := chain(t, 3)
	c[1] = bytes.Replace(c[1], []byte(`"scout"`), []byte(`"spy"`), 1)
	expectBreak(t, c, 1)
}

func TestTamperBrokenLink(t *testing.T) {
	c := chain(t, 3)
	c = append(c[:1], c[2:]...) // drop the middle record
	expectBreak(t, c, 1)
}

func TestTamperWrongGenesis(t *testing.T) {
	rec := []byte(`{"seq":0,"actor":"founder","action":"boot"}`)
	sealed, err := Seal(rec, "1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	expectBreak(t, []json.RawMessage{sealed}, 0)
}

func TestTamperMissingHash(t *testing.T) {
	expectBreak(t, []json.RawMessage{json.RawMessage(`{"prev_hash":"` + Genesis + `"}`)}, 0)
}

// TestContractFixtureChain verifies the contract's valid chain-<n>.json audit
// fixtures, one record per file, as one chain in name order.
func TestContractFixtureChain(t *testing.T) {
	pattern := filepath.Join("..", "..", "contract", "fixtures", "schemas", "audit-record", "valid", "chain-*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no audit chain fixtures yet")
	}
	sort.Strings(files)
	records := make([]json.RawMessage, 0, len(files))
	for _, f := range files {
		raw, err := os.ReadFile(f) // #nosec G304 -- fixed test directory
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, raw)
	}
	if err := Verify(records); err != nil {
		t.Fatal(err)
	}
}
