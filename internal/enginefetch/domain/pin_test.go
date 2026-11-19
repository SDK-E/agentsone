// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

const goodSum = "a982d38030b97e750848add2cbb2df6d9a70d5544a22ca9b216a349ba25865e8"

func validPin() domain.Pin {
	return domain.Pin{
		Engine:  "claude-code",
		Version: "2.1.328",
		BaseURL: "https://downloads.claude.ai/claude-code-releases",
		Artifacts: map[string]domain.Artifact{
			"linux/amd64": {Platform: "linux-x64", Binary: "claude", SHA256: goodSum, Size: 373019152},
		},
	}
}

func TestPinned_ShipsAValidPinForBothMachineArchitectures(t *testing.T) {
	t.Parallel()
	p, err := domain.Pinned()
	if err != nil {
		t.Fatalf("Pinned() error = %v", err)
	}
	if p.Engine != "claude-code" || p.BaseURL != "https://downloads.claude.ai/claude-code-releases" {
		t.Fatalf("Pinned() = %+v, want claude-code from downloads.claude.ai", p)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		if _, err := p.For("linux", arch); err != nil {
			t.Errorf("For(linux, %s) error = %v", arch, err)
		}
	}
}

func TestPinValidate_RejectsBrokenPins(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(p *domain.Pin)
	}{
		{"engine missing", func(p *domain.Pin) { p.Engine = "" }},
		{"engine with a slash", func(p *domain.Pin) { p.Engine = "claude/code" }},
		{"version not exact", func(p *domain.Pin) { p.Version = "latest" }},
		{"version with a path", func(p *domain.Pin) { p.Version = "2.1.328/../x" }},
		{"base url not https", func(p *domain.Pin) { p.BaseURL = "http://downloads.claude.ai/claude-code-releases" }},
		{"base url ends in a slash", func(p *domain.Pin) { p.BaseURL += "/" }},
		{"no artifacts", func(p *domain.Pin) { p.Artifacts = nil }},
		{"checksum too short", func(p *domain.Pin) { setArtifact(p, func(a *domain.Artifact) { a.SHA256 = goodSum[:63] }) }},
		{"checksum upper case", func(p *domain.Pin) { setArtifact(p, func(a *domain.Artifact) { a.SHA256 = strings.ToUpper(goodSum) }) }},
		{"size zero", func(p *domain.Pin) { setArtifact(p, func(a *domain.Artifact) { a.Size = 0 }) }},
		{"platform with a slash", func(p *domain.Pin) { setArtifact(p, func(a *domain.Artifact) { a.Platform = "linux/x64" }) }},
		{"binary with a slash", func(p *domain.Pin) { setArtifact(p, func(a *domain.Artifact) { a.Binary = "../claude" }) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := validPin()
			tt.edit(&p)
			if err := p.Validate(); !errors.Is(err, domain.ErrInvalidPin) {
				t.Fatalf("Validate() error = %v, want %v", err, domain.ErrInvalidPin)
			}
		})
	}
}

func setArtifact(p *domain.Pin, edit func(a *domain.Artifact)) {
	arts := map[string]domain.Artifact{}
	for k, a := range p.Artifacts {
		edit(&a)
		arts[k] = a
	}
	p.Artifacts = arts
}

func TestPinValidate_AcceptsAGoodPin(t *testing.T) {
	t.Parallel()
	if err := validPin().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestPinFor_RefusesAnUnpinnedPlatform(t *testing.T) {
	t.Parallel()
	_, err := validPin().For("linux", "riscv64")
	if !errors.Is(err, domain.ErrUnsupportedPlatform) {
		t.Fatalf("For(linux, riscv64) error = %v, want %v", err, domain.ErrUnsupportedPlatform)
	}
}

func TestPinURL_PointsAtTheVersionAndPlatform(t *testing.T) {
	t.Parallel()
	p := validPin()
	a, _ := p.For("linux", "amd64")
	want := "https://downloads.claude.ai/claude-code-releases/2.1.328/linux-x64/claude"
	if got := p.URL(a); got != want {
		t.Fatalf("URL() = %q, want %q", got, want)
	}
}

func TestBinaryPath_IsUnderTheEngineAndVersion(t *testing.T) {
	t.Parallel()
	want := "/var/lib/agentsone-engines/claude-code/2.1.328/claude"
	if got := domain.BinaryPath(domain.DefaultRoot, validPin()); got != want {
		t.Fatalf("BinaryPath() = %q, want %q", got, want)
	}
}
