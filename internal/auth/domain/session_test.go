// SPDX-License-Identifier: MIT

package domain_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func TestNewSession_StoresOnlyTheHashOfTheCookieSecret(t *testing.T) {
	t.Parallel()
	s, secret, err := domain.NewSession(randomBytes(3), "ada", epoch)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != domain.SessionID(secret) || strings.Contains(s.ID, secret) {
		t.Fatalf("id %q for secret %q", s.ID, secret)
	}
	if !s.ExpiresAt.Equal(epoch.Add(domain.SessionTTL)) {
		t.Fatalf("expires %v", s.ExpiresAt)
	}
}

func TestNewSession_FailsWhenRandomnessFails(t *testing.T) {
	t.Parallel()
	if _, _, err := domain.NewSession(bytes.NewReader(nil), "ada", epoch); err == nil {
		t.Fatal("no error")
	}
}

func TestSession_LiveUntilExpiry(t *testing.T) {
	t.Parallel()
	s, _, _ := domain.NewSession(randomBytes(3), "ada", epoch)
	if !s.Live(epoch.Add(domain.SessionTTL - time.Second)) {
		t.Fatal("dead before expiry")
	}
	if s.Live(epoch.Add(domain.SessionTTL)) {
		t.Fatal("live at expiry")
	}
}

func TestCheckHumanName_FollowsTheContractPattern(t *testing.T) {
	t.Parallel()
	for name, ok := range map[string]bool{"ada": true, "a-1": true, "": false, "Ada": false, "1ab": false, "root user": false} {
		err := domain.CheckHumanName(name)
		if ok != (err == nil) || (!ok && !errors.Is(err, core.ErrInvalid)) {
			t.Errorf("%q: err %v", name, err)
		}
	}
}

func TestHuman_CallerIsAHumanWithoutRootOrLinuxUID(t *testing.T) {
	t.Parallel()
	c := domain.Human{Name: "ada", UID: 1000}.Caller()
	if c.Kind != core.CallerHuman || c.Name != "ada" || c.UID != -1 || c.IsRoot || c.Assurance != core.AssuranceHard {
		t.Fatalf("caller %+v", c)
	}
}
