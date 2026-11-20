// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/daemontest"
	"github.com/SDK-E/agentsone/internal/store"
)

// Integration tests: a token issued through the API is revoked through the
// API by the id the issue returned, and the next call with it is refused on
// both listeners.

// tokenID is the contract's TokenId pattern.
var tokenID = regexp.MustCompile(`^tok_[0-9A-HJKMNP-TV-Z]{26}$`)

func TestToken_ARevokedTokenIsRefusedAtOnceOnBothListeners(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	tok := issueToken(t, d.AsAgent("founder"), "founder")
	if !tokenID.MatchString(tok.Id) {
		t.Fatalf("issued token id = %q, want the contract's tok_ form", tok.Id)
	}
	for _, network := range []string{"tcp", "unix"} {
		if code, body := bearerGet(t, d, network, tok.Token, "/agents"); code != http.StatusOK {
			t.Fatalf("GET /agents on %s with the new token: status %d: %s", network, code, body)
		}
	}

	if code, body := alice.Try(http.MethodDelete, "/tokens/"+tok.Id, nil); code != http.StatusNoContent {
		t.Fatalf("DELETE /tokens/%s as a human: status %d, want 204: %s", tok.Id, code, body)
	}

	for _, network := range []string{"tcp", "unix"} {
		code, body := bearerGet(t, d, network, tok.Token, "/agents")
		if code != http.StatusUnauthorized {
			t.Errorf("GET /agents on %s with the revoked token: status %d, want 401: %s", network, code, body)
		}
		noSecret(t, tok, body)
	}
	if !audited(alice, "token.revoke", "human:alice", "token:"+tok.Id) {
		t.Errorf("the revoke is not in the audit trail as alice's on token:%s", tok.Id)
	}
}

func TestToken_RevokingAnUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	tok := issueToken(t, d.AsAgent("founder"), "founder")

	code, body := alice.Try(http.MethodDelete, "/tokens/tok_01JD7ZK3M0000000000000000Z", nil)

	if code != http.StatusNotFound {
		t.Fatalf("DELETE an unknown token id: status %d, want 404: %s", code, body)
	}
	noSecret(t, tok, string(body))
	if code, body := bearerGet(t, d, "tcp", tok.Token, "/agents"); code != http.StatusOK {
		t.Fatalf("the founder's token after revoking another id: status %d, want 200: %s", code, body)
	}
}

// Only a human or the token's own subject may revoke it: another agent is
// refused, and the token keeps working.
func TestToken_AnotherAgentMayNotRevokeTheToken(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	d.SignInHuman("alice")
	founder := d.AsAgent("founder")
	tok := issueToken(t, founder, "founder")

	code, body := d.AsAgent("router").Try(http.MethodDelete, "/tokens/"+tok.Id, nil)

	if code != http.StatusForbidden {
		t.Fatalf("DELETE the founder's token as the router: status %d, want 403: %s", code, body)
	}
	noSecret(t, tok, string(body))
	if code, body := bearerGet(t, d, "tcp", tok.Token, "/agents"); code != http.StatusOK {
		t.Fatalf("the founder's token after the refused revoke: status %d, want 200: %s", code, body)
	}
	// The subject may revoke its own token.
	if code, body := founder.Try(http.MethodDelete, "/tokens/"+tok.Id, nil); code != http.StatusNoContent {
		t.Fatalf("DELETE its own token as the founder: status %d, want 204: %s", code, body)
	}
	if code, body := bearerGet(t, d, "unix", tok.Token, "/agents"); code != http.StatusUnauthorized {
		t.Fatalf("the token the founder revoked: status %d, want 401: %s", code, body)
	}
}

// A store from before the fix holds a live token under a hex id. The
// upgrade revokes it, writes one audit record naming it by id, and its
// value gets a clean 401 on both listeners. Sign-in sessions stay.
func TestToken_TheUpgradeRevokesAndAuditsATokenOfTheEarlierForm(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	d.SignInHuman("alice")
	d.Stop()
	// The test token's parts, written apart so no line reads as a leaked credential.
	const legacyID = "0123456789abcdef01234567"
	const legacyTail = "earlier-form-tail"
	db := openCore(t, d)
	sum := sha256.Sum256([]byte(legacyTail))
	now := d.Clock.Now().UTC()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		// Back to the schema before migration 0002, with a live token in it.
		{`DELETE FROM schema_migrations WHERE module = 'auth' AND version = 2`, nil},
		{`DROP TABLE auth_legacy_revokes`, nil},
		{`INSERT INTO auth_tokens (id, hash, subject_kind, subject_name, scopes, issued_at, expires_at, revoked_at)
			VALUES (?, ?, 'agent', 'founder', '["agents:read"]', ?, ?, NULL)`,
			[]any{legacyID, sum[:], now.Format(time.RFC3339Nano), now.Add(24 * time.Hour).Format(time.RFC3339Nano)}},
	} {
		if _, err := db.SQL().ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	sessions := count(t, db, `SELECT COUNT(*) FROM auth_sessions`)

	d = d.Restart()

	value := "aot_" + legacyID + "_" + legacyTail
	for _, network := range []string{"tcp", "unix"} {
		code, body := bearerGet(t, d, network, value, "/agents")
		if code != http.StatusUnauthorized {
			t.Errorf("GET /agents on %s with the earlier-form token: status %d, want 401: %s", network, code, body)
		}
		if strings.Contains(body, legacyTail) {
			t.Errorf("the refusal holds the secret: %s", body)
		}
	}
	var records int
	for _, r := range d.AsAgent("founder").Audit() {
		if r.Action == "token.revoke" && r.Actor.Name == "auth" && string(r.Target.Kind)+":"+r.Target.Id == "token:"+legacyID {
			records++
		}
		if raw, err := json.Marshal(r); err != nil || strings.Contains(string(raw), legacyTail) {
			t.Errorf("audit record %s holds the secret (%v)", r.Hash, err)
		}
	}
	if records != 1 {
		t.Errorf("audit records of the upgrade's revoke of %s = %d, want 1", legacyID, records)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM auth_legacy_revokes`); n != 0 {
		t.Errorf("%d revokes wait for their audit record after the start, want none", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM auth_sessions`); n != sessions || n == 0 {
		t.Errorf("sign-in sessions after the upgrade = %d, want the %d from before", n, sessions)
	}
}

func count(t *testing.T, db *store.DB, query string) int {
	t.Helper()
	var n int
	if err := db.SQL().QueryRowContext(t.Context(), query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// issueToken issues an agents:read token for agent through the API.
func issueToken(t *testing.T, issuer *daemontest.Client, agent string) api.IssuedToken {
	t.Helper()
	var tok api.IssuedToken
	issuer.Do(http.MethodPost, "/agents/"+agent+"/tokens", api.TokenCreate{Label: "ci tool", Scopes: []api.Scope{"agents:read"}}, &tok)
	if tok.Token == "" || strings.Contains(tok.Id, tok.Token) {
		t.Fatalf("issued token = id %q and a value of %d bytes, want a secret apart from the id", tok.Id, len(tok.Token))
	}
	return tok
}

// bearerGet calls path with token on the daemon's TCP listener or its Unix
// socket, the way a tool outside the society does.
func bearerGet(t *testing.T, d *daemontest.Daemon, network, token, path string) (int, string) {
	t.Helper()
	addr := d.TCPAddr()
	if network == "unix" {
		addr = d.Config.SocketPath()
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(t.Context(), daemontest.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agentsoned/v1"+path, nil)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatalf("GET %s on %s: %v", path, network, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("GET %s: close answer: %v", path, err)
		}
	}()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: read answer: %v", path, err)
	}
	return resp.StatusCode, string(raw)
}

// noSecret fails the test when body holds the token's secret value.
func noSecret(t *testing.T, tok api.IssuedToken, body string) {
	t.Helper()
	if strings.Contains(body, tok.Token) {
		t.Errorf("an answer holds the token's secret value: %s", body)
	}
}
