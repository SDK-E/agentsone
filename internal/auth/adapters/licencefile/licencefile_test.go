// SPDX-License-Identifier: MIT

package licencefile_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/auth/adapters/licencefile"
	"github.com/SDK-E/agentsone/internal/core"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "licence.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEngineLicence_ReadsTheShippedFile(t *testing.T) {
	t.Parallel()
	p := write(t, `{"engine":"claude-code","version":"2026-11","text":"Terms."}`)
	e, v, text, err := licencefile.New(p).EngineLicence(context.Background())
	if err != nil || e != "claude-code" || v != "2026-11" || text != "Terms." {
		t.Fatalf("got %q %q %q %v", e, v, text, err)
	}
}

func TestEngineLicence_MissingOrIncompleteIsUnavailable(t *testing.T) {
	t.Parallel()
	for name, p := range map[string]string{
		"missing":    filepath.Join(t.TempDir(), "none.json"),
		"incomplete": write(t, `{"engine":"claude-code"}`),
	} {
		if _, _, _, err := licencefile.New(p).EngineLicence(context.Background()); !errors.Is(err, core.ErrUnavailable) {
			t.Errorf("%s: err %v", name, err)
		}
	}
}

func TestEngineLicence_MalformedFileIsAnError(t *testing.T) {
	t.Parallel()
	_, _, _, err := licencefile.New(write(t, `{`)).EngineLicence(context.Background())
	if err == nil || errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("err %v", err)
	}
}
