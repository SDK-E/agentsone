// SPDX-License-Identifier: MIT

package router

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/gatewaymint"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

type fakeProfiles struct{ profiles []core.AgentProfile }

func (f fakeProfiles) Profiles(context.Context) ([]core.AgentProfile, error) { return f.profiles, nil }

type fakeChat struct {
	agent, text, assignment string
	err                     error
}

func (f *fakeChat) PostChat(_ context.Context, agent, text, assignment string) (string, error) {
	f.agent, f.text, f.assignment = agent, text, assignment
	return "msg_1", f.err
}

type fakeMint struct{ grant core.GatewayGrant }

func (f *fakeMint) Mint(_ context.Context, g core.GatewayGrant) (string, error) {
	f.grant = g
	return "tok", nil
}

func TestProfileAdapter_MapsProfilesAndMarksRouters(t *testing.T) {
	ports := core.NewPorts()
	ports.Profiles = fakeProfiles{profiles: []core.AgentProfile{
		{Name: "main", Template: "small-seed/router", Kind: core.AgentChat, State: "sleeping", Team: "boss", DisplayName: "Main", Role: "Routes"},
		{Name: "router", Kind: core.AgentChat},
		{Name: "docs-router", Kind: core.AgentChat},
		{Name: "legal", Template: "acme/router", Kind: core.AgentChat},
		{Name: "builder", Kind: core.AgentChat, State: "working", Team: "boss", DisplayName: "Builder", Role: "Builds"},
	}}

	got, err := profileAdapter{ports: ports}.Profiles(context.Background())

	if err != nil || len(got) != 5 {
		t.Fatalf("got %v, %v", got, err)
	}
	if !got[0].Router || !got[1].Router || !got[2].Router || !got[3].Router || got[4].Router {
		t.Fatalf("router flags = %+v", got)
	}
	if b := got[4]; b.Agent != "builder" || b.Role != "Builds" || b.State != "working" || b.Team != "boss" || b.Kind != "chat" || b.DisplayName != "Builder" {
		t.Fatalf("builder = %+v", b)
	}
}

func TestProfileAdapter_PortDownIsAnError(t *testing.T) {
	_, err := profileAdapter{ports: core.NewPorts()}.Profiles(context.Background())
	if !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

func TestCredentialAdapter_IssuesAShortGatewayToken(t *testing.T) {
	ports, mint := core.NewPorts(), &fakeMint{}
	ports.GatewayTokens = mint

	got, err := credentialAdapter{ports: ports}.TokenFor(context.Background(), "router")

	if err != nil || got != "tok" {
		t.Fatalf("got %q, %v", got, err)
	}
	if want := (core.GatewayGrant{Subject: "system:router", TTL: 5 * time.Minute}); mint.grant != want {
		t.Fatalf("minted %+v, want %+v", mint.grant, want)
	}
	if _, err := (credentialAdapter{ports: core.NewPorts()}).TokenFor(context.Background(), "router"); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

// The daemon's real minter signs the router's token, and the gateway's own
// verifier reads it as a system caller with the gateway audience.
func TestCredentialAdapter_RealMinterSignsATokenTheGatewayAccepts(t *testing.T) {
	key := bytes.Repeat([]byte{0x2c}, gatewaymint.KeySize)
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatal(err)
	}
	f, ports := coretest.NewFakes()
	ports.GatewayTokens = &gatewaymint.Minter{KeyFile: path, Clock: f.Clock, Ports: ports}

	tok, err := credentialAdapter{ports: ports}.TokenFor(context.Background(), "router")
	if err != nil {
		t.Fatal(err)
	}
	c, err := gatewaytoken.Verify(key, tok, f.Clock.Now())
	if err != nil {
		t.Fatalf("gateway refused the router's token: %v", err)
	}
	if c.Agent != "system:router" || c.Kind != gatewaytoken.KindSystem || c.Chart != "" || c.Audience != gatewaytoken.AudienceGateway ||
		c.ExpiresAt-c.IssuedAt != int64(tokenTTL/time.Second) {
		t.Fatalf("claims = %+v", c)
	}
}

func TestChatAdapter_PostsAndReturnsThePath(t *testing.T) {
	ports, chat := core.NewPorts(), &fakeChat{}
	ports.Chat = chat

	ref, err := chatAdapter{ports: ports}.OpenChat(context.Background(), "builder", "do it", "asg_1")

	if err != nil || ref.Path != "/v1/agents/builder/chat" || ref.MessageID != "msg_1" {
		t.Fatalf("ref = %+v, %v", ref, err)
	}
	if chat.agent != "builder" || chat.text != "do it" || chat.assignment != "asg_1" {
		t.Fatalf("posted %+v", chat)
	}
}

func TestChatAdapter_KeepsTheCauseOfAFailure(t *testing.T) {
	ports := core.NewPorts()
	ports.Chat = &fakeChat{err: core.ErrFrozen}
	if _, err := (chatAdapter{ports: ports}).OpenChat(context.Background(), "a", "t", "id"); !errors.Is(err, core.ErrFrozen) {
		t.Fatalf("error = %v", err)
	}
	if _, err := (chatAdapter{ports: core.NewPorts()}).OpenChat(context.Background(), "a", "t", "id"); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

func TestModuleConfigDefaults(t *testing.T) {
	if c := defaultConfig(); c.GatewayURL != "http://edge:4000" || c.TimeoutSeconds != 20 {
		t.Fatalf("defaults = %+v", c)
	}
}
