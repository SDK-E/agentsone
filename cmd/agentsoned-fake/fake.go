// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/core"
)

const (
	variantQuery  = "fixture"
	variantHeader = "X-Fixture-Variant"
	servedHeader  = "X-Fixture-Variant-Served"
	corsMethods   = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	corsHeaders   = "Content-Type, Authorization, " + variantHeader + ", Last-Event-ID"
)

// Options configure the fake daemon handler.
type Options struct {
	Fixtures contracttest.APIFixtures
	// Match maps a request to a contract operation; api.Match in production.
	Match          func(method, path string) (api.Route, bool)
	AllowedOrigins []string
	// StreamInterval is the pause between replayed stream events.
	StreamInterval time.Duration
}

type fake struct{ Options }

const defaultStreamInterval = 2 * time.Second

// NewHandler serves golden fixtures for every operation of the contract.
// Match defaults to api.Match and StreamInterval to two seconds.
func NewHandler(o Options) http.Handler {
	if o.Match == nil {
		o.Match = api.Match
	}
	if o.StreamInterval <= 0 {
		o.StreamInterval = defaultStreamInterval
	}
	return &fake{o}
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.allowOrigin(w, r)
	if isPreflight(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	route, ok := f.Match(r.Method, r.URL.Path)
	if !ok {
		core.WriteProblem(w, r, core.NewProblem(http.StatusNotFound, "not-found",
			fmt.Sprintf("%s %s is not in the contract", r.Method, r.URL.Path)))
		return
	}
	fx, problem := f.pick(route.OperationID, requestedVariant(r))
	if problem != nil {
		core.WriteProblem(w, r, problem)
		return
	}
	w.Header().Set(servedHeader, servedName(fx.Variant))
	if route.Stream && fx.Status < http.StatusMultipleChoices {
		f.stream(w, r, fx)
		return
	}
	writeFixture(w, fx)
}

func (f *fake) allowOrigin(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || !slices.Contains(f.AllowedOrigins, origin) {
		return
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Expose-Headers", servedHeader)
	h.Add("Vary", "Origin")
	if isPreflight(r) {
		h.Set("Access-Control-Allow-Methods", corsMethods)
		h.Set("Access-Control-Allow-Headers", corsHeaders)
		h.Set("Access-Control-Max-Age", "600")
	}
}

func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

// variantChoice is the requested fixture variant. A query names it for one
// request, so it is strict. A header is set once for a whole page, so an
// operation without that variant falls back to its default fixture.
type variantChoice struct {
	name   string
	strict bool
}

func requestedVariant(r *http.Request) variantChoice {
	if v := r.URL.Query().Get(variantQuery); v != "" {
		return variantChoice{name: v, strict: true}
	}
	return variantChoice{name: r.Header.Get(variantHeader)}
}

func servedName(variant string) string {
	if variant == "" {
		return "default"
	}
	return variant
}

func (f *fake) pick(operationID string, choice variantChoice) (contracttest.APIFixture, *core.Problem) {
	variants := f.Fixtures[operationID]
	if len(variants) == 0 {
		return contracttest.APIFixture{}, core.NotImplemented(operationID)
	}
	fx, ok := variants[choice.name]
	if !ok && !choice.strict {
		fx, ok = variants[""]
	}
	if !ok {
		variant := choice.name
		return contracttest.APIFixture{}, core.NewProblem(http.StatusNotFound, "not-found",
			fmt.Sprintf("no fixture variant %q for %s; available: %s", variant, operationID, variantNames(variants)))
	}
	return fx, nil
}

func variantNames(variants map[string]contracttest.APIFixture) string {
	names := make([]string, 0, len(variants))
	for v := range variants {
		if v != "" {
			names = append(names, v)
		}
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

func writeFixture(w http.ResponseWriter, fx contracttest.APIFixture) {
	if len(bytes.TrimSpace(fx.Body)) == 0 {
		w.WriteHeader(fx.Status)
		return
	}
	contentType := "application/json"
	if fx.Status >= http.StatusBadRequest {
		contentType = "application/problem+json"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(fx.Status)
	_, _ = w.Write(fx.Body) // the status is sent; a failed write means the client left
}

// stream replays the fixture's events as server-sent events until the
// client disconnects.
func (f *fake) stream(w http.ResponseWriter, r *http.Request, fx contracttest.APIFixture) {
	events, err := streamEvents(fx.Body)
	if err != nil {
		core.WriteProblem(w, r, core.NewProblem(http.StatusInternalServerError, "bad-fixture", err.Error()))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher := http.NewResponseController(w)
	ticker := time.NewTicker(f.StreamInterval)
	defer ticker.Stop()
	for sent := 0; ; sent++ {
		e := events[sent%len(events)]
		if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", sent+1, e.kind, e.data); err != nil {
			return
		}
		if err := flusher.Flush(); err != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

type sseEvent struct {
	kind string
	data []byte
}

// streamEvents reads a stream fixture body: a JSON array of events, or one
// event on its own.
func streamEvents(body json.RawMessage) ([]sseEvent, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		raw = []json.RawMessage{body}
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("stream fixture has no events")
	}
	events := make([]sseEvent, 0, len(raw))
	for _, r := range raw {
		e, err := toSSE(r)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}

func toSSE(raw json.RawMessage) (sseEvent, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return sseEvent{}, fmt.Errorf("stream fixture event: %w", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return sseEvent{}, fmt.Errorf("stream fixture event: %w", err)
	}
	kind := head.Type
	if kind == "" {
		kind = "message"
	}
	return sseEvent{kind: kind, data: compact.Bytes()}, nil
}
