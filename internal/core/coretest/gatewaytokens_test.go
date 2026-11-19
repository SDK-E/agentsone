// SPDX-License-Identifier: MIT

package coretest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
)

func TestGatewayTokensRecordsGrants(t *testing.T) {
	f, p := coretest.NewFakes()
	g := core.GatewayGrant{Subject: "mira", ChartPath: "founder/mira", Defence: true, TTL: time.Minute}
	tok, err := p.GatewayTokens.Mint(context.Background(), g)
	if err != nil || tok != "fake-gateway-token-mira" {
		t.Fatalf("Mint = %q, %v", tok, err)
	}
	if got := f.GatewayTokens.Grants(); len(got) != 1 || got[0] != g {
		t.Fatalf("Grants() = %+v, want [%+v]", got, g)
	}
	f.GatewayTokens.Err = core.ErrUnavailable
	if _, err := p.GatewayTokens.Mint(context.Background(), g); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("Mint with Err set = %v", err)
	}
}
