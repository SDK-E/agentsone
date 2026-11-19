// SPDX-License-Identifier: MIT

package domain_test

import (
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

func TestParseProblem_ReadsProblemDetails(t *testing.T) {
	t.Parallel()
	body := `{"type":"urn:agentsone:problem:not-below-actor","title":"Not below you","status":403,"detail":"mira is not below founder"}`

	p := domain.ParseProblem(403, []byte(body))

	if p.Type != "urn:agentsone:problem:not-below-actor" || p.Title != "Not below you" || p.Detail != "mira is not below founder" || p.Status != 403 {
		t.Fatalf("problem = %+v", p)
	}
}

func TestParseProblem_FallsBackToTheStatusWhenTheBodyIsNotAProblem(t *testing.T) {
	t.Parallel()

	p := domain.ParseProblem(502, []byte("upstream gone"))

	if p.Status != 502 || p.Title != "Bad Gateway" || p.Detail != "upstream gone" || p.Type != "about:blank" {
		t.Fatalf("problem = %+v", p)
	}
}

func TestParseProblem_TrimsALongPlainBody(t *testing.T) {
	t.Parallel()

	p := domain.ParseProblem(500, []byte(strings.Repeat("x", 5000)))

	if len(p.Detail) > 300 {
		t.Fatalf("detail has %d bytes, want at most 300", len(p.Detail))
	}
}

func TestProblemText_NamesTypeAndDetail(t *testing.T) {
	t.Parallel()
	p := domain.Problem{Type: "urn:agentsone:problem:conflict", Title: "Conflict", Status: 409, Detail: "name taken"}

	got := p.Error()

	for _, want := range []string{"Conflict", "name taken", "urn:agentsone:problem:conflict", "409"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, missing %q", got, want)
		}
	}
}
