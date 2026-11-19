// SPDX-License-Identifier: MIT

package contracttest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
)

// defaultValidator is compiled once per test binary from the embedded
// contract. It is read-only after construction, so sharing it across tests
// only saves recompiling the same schemas.
var defaultValidator = sync.OnceValues(func() (*Validator, error) {
	return NewValidator(api.Spec)
})

// CheckResponse fails t when rec is not a response the contract allows for req.
func CheckResponse(t testing.TB, req *http.Request, rec *httptest.ResponseRecorder) {
	t.Helper()
	v, err := defaultValidator()
	if err != nil {
		t.Errorf("load embedded contract: %v", err)
		return
	}
	v.Check(t, req, rec)
}

// Check fails t when rec is not a response this validator's contract allows for req.
func (v *Validator) Check(t testing.TB, req *http.Request, rec *httptest.ResponseRecorder) {
	t.Helper()
	op, ok := v.match(req.Method, req.URL.Path)
	if !ok {
		t.Errorf("%s %s is not in the contract", req.Method, req.URL.Path)
		return
	}
	if err := v.ValidateResponse(op.id, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Bytes()); err != nil {
		t.Errorf("%s %s: %v", req.Method, req.URL.Path, err)
	}
}

// match finds the operation for a concrete path; literal segments win over
// parameters, as in net/http.
func (v *Validator) match(method, path string) (operation, bool) {
	var best operation
	bestScore := -1
	for _, op := range v.routes {
		if !strings.EqualFold(op.method, method) {
			continue
		}
		if score, ok := literalMatches(op.path, path); ok && score > bestScore {
			best, bestScore = op, score
		}
	}
	return best, bestScore >= 0
}

func literalMatches(template, path string) (int, bool) {
	want := strings.Split(strings.Trim(template, "/"), "/")
	got := strings.Split(strings.Trim(path, "/"), "/")
	if len(want) != len(got) {
		return 0, false
	}
	literal := 0
	for i, w := range want {
		isParam := strings.HasPrefix(w, "{") && strings.HasSuffix(w, "}")
		switch {
		case isParam && got[i] != "":
		case w == got[i]:
			literal++
		default:
			return 0, false
		}
	}
	return literal, true
}
