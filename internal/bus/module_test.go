// SPDX-License-Identifier: MIT

package bus_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SDK-E/agentsone/internal/bus"
	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/core/moduletest"
)

func TestModuleOwnsItsTags(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := bus.New(deps)
	if m.Name() != "bus" {
		t.Fatalf("name %q", m.Name())
	}
	moduletest.AssertOwnsTags(t, m, "bus")
}

// boot runs the lifecycle steps the daemon runs, up to Start.
func boot(t *testing.T) (*bus.Module, core.Deps, *coretest.Fakes) {
	t.Helper()
	deps, fakes := coretest.NewDeps(t)
	fakes.Agents.Add(core.Agent{Name: "builder", Kind: core.AgentChat})
	fakes.Agents.Add(core.Agent{Name: "intern", Kind: core.AgentChat, Manager: "builder"})
	m := bus.New(deps)
	m.Provide(deps.Ports)
	if err := m.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	return m, deps, fakes
}

func TestModule_MigratesTwiceAndServesChannelsOnTheRealStore(t *testing.T) {
	m, _, _ := boot(t)
	if err := m.Migrate(t.Context()); err != nil {
		t.Fatalf("second Migrate() error = %v, want it to be safe to run again", err)
	}
	mux := moduletest.Mount(m)

	create := httptest.NewRequest(http.MethodPost, "/v1/bus/channels", bytes.NewReader([]byte(`{"name":"general","kind":"public"}`)))
	create.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/bus/channels", nil))
	var list api.ChannelList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK || len(list.Items) != 1 || list.Items[0].Name != "general" {
		t.Fatalf("list status %d, %+v, %v: %s", rec.Code, list, err, rec.Body)
	}
}

func TestModule_ProvidesThePortThatTheRunnerProtocolSendsThrough(t *testing.T) {
	m, deps, fakes := boot(t)
	if deps.Ports.Bus == nil {
		t.Fatal("Provide() did not set Ports.Bus")
	}

	sent, err := deps.Ports.Bus.Send(t.Context(), core.BusMessage{From: "agent:builder", To: "agent:intern", Body: "Wake up."})

	if err != nil || sent.ID == "" {
		t.Fatalf("Send() = %+v, %v", sent, err)
	}
	if wakes := fakes.Waker.Wakes(); len(wakes) != 1 || wakes[0].Agent != "intern" {
		t.Fatalf("wakes = %+v", wakes)
	}
	rec := httptest.NewRecorder()
	moduletest.Mount(m).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/bus/messages", nil))
	var list api.BusMessageList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 1 || list.Items[0].Id != sent.ID {
		t.Fatalf("history = %+v, %v: %s", list, err, rec.Body)
	}
}
