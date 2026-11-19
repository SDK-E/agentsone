// SPDX-License-Identifier: MIT

package sdk_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/runners/conformance"
	"github.com/SDK-E/agentsone/runners/echo"
	"github.com/SDK-E/agentsone/runners/sdk"
	"github.com/SDK-E/agentsone/runners/sdk/sdktest"
)

func echoSubject(opts func(*sdk.Options)) conformance.Subject {
	return conformance.Subject{
		Name: "sdk-echo",
		Start: conformance.InProcess(func(ctx context.Context, socket string) error {
			o := echo.Options()
			o.Socket = socket
			if opts != nil {
				opts(&o)
			}
			return sdk.Run(ctx, o, echo.New())
		}),
		Prompts: conformance.Prompts{
			Plain:  "Hello echo",
			Hold:   echo.CmdHold,
			Budget: echo.CmdBudget,
			Long:   func(n int) string { return echo.CmdLong + " " + strconv.Itoa(n) },
		},
		SummaryInOutput: true,
	}
}

// TestConformanceWithEcho runs every PROTOCOL.md rule against a runner built
// on the SDK (TESTING B4c).
func TestConformanceWithEcho(t *testing.T) {
	conformance.Run(t, echoSubject(nil))
}

// TestHeartbeatUptime proves B1 with a fake clock: the first heartbeat after
// 15 seconds says uptimeMs 15000 (PROTOCOL.md transcript, line 4 without the
// jitter of a real clock).
func TestHeartbeatUptime(t *testing.T) {
	clock := sdktest.NewClock(time.Date(2026, 11, 19, 19, 0, 0, 0, time.UTC))
	h := conformance.Start(t, echoSubject(func(o *sdk.Options) { o.Clock = clock }), func(w *conformance.WelcomeSpec) {
		w.HeartbeatIntervalMs = 15000
		w.HeartbeatTimeoutMs = 45000
	})
	h.Handshake()
	<-clock.Registered
	clock.Advance(15 * time.Second)
	got := h.RecvRaw()
	want := `{"jsonrpc":"2.0","method":"heartbeat","params":{"uptimeMs":15000}}`
	if got.String() != want {
		t.Fatalf("heartbeat = %s, want %s", got, want)
	}
	clock.Advance(15 * time.Second)
	got = h.RecvRaw()
	if got.Field("uptimeMs") != "30000" {
		t.Fatalf("second heartbeat = %s, want uptimeMs 30000", got)
	}
}
