// SPDX-License-Identifier: MIT

package domain_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/auth/adapters/licencefile"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

const approvedNotice = "Claude Code is proprietary software by Anthropic PBC. Agents One does not include it. " +
	"If you accept, this machine downloads it for you, unchanged. Your use of it is subject to Anthropic's terms " +
	"for your account, listed at https://code.claude.com/docs/en/legal-and-compliance."

func TestLicenceNotice_IsTheApprovedText(t *testing.T) {
	t.Parallel()
	if domain.LicenceNotice != approvedNotice {
		t.Fatalf("LicenceNotice = %q, want the approved text", domain.LicenceNotice)
	}
}

func TestLicenceJSON_CarriesThePinAndTheNoticeAndNoAcceptance(t *testing.T) {
	t.Parallel()
	p := validPin()
	b, err := p.LicenceJSON()
	if err != nil {
		t.Fatalf("LicenceJSON() error = %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("output is not a JSON object of strings: %v", err)
	}
	want := map[string]string{"engine": p.Engine, "version": p.Version, "text": domain.LicenceNotice}
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want exactly engine, version, text", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if strings.Contains(strings.ToLower(string(b)), "accept\"") {
		t.Errorf("output must never carry an acceptance: %s", b)
	}
}

func TestLicenceJSON_RefusesAnInvalidPin(t *testing.T) {
	t.Parallel()
	p := validPin()
	p.Version = "latest"
	if _, err := p.LicenceJSON(); !errors.Is(err, domain.ErrInvalidPin) {
		t.Fatalf("LicenceJSON() error = %v, want ErrInvalidPin", err)
	}
}

func TestLicenceJSON_OfThePinnedEngineIsReadByTheAuthLicenceAdapter(t *testing.T) {
	t.Parallel()
	pin, err := domain.Pinned()
	if err != nil {
		t.Fatalf("Pinned() error = %v", err)
	}
	b, err := pin.LicenceJSON()
	if err != nil {
		t.Fatalf("LicenceJSON() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "engine", "licence.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	engine, version, text, err := licencefile.New(path).EngineLicence(context.Background())
	if err != nil {
		t.Fatalf("EngineLicence() error = %v", err)
	}
	if engine != pin.Engine || version != pin.Version || text != domain.LicenceNotice {
		t.Fatalf("EngineLicence() = %q %q %q, want the pin and the notice", engine, version, text)
	}
}
