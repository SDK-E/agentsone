// SPDX-License-Identifier: MIT

package sdk_test

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/runners/sdk"
)

const (
	turnA = "trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G"
	turnB = "trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H"

	welcomeJSON = `{"protocolVersion":"1.0","agent":{"name":"founder","kind":"always-on","isRoot":true,"uid":0,"team":"founder","manager":null,` +
		`"oneLine":"Goal: test. Now: starting up.","charter":"c","goals":["g"]},` +
		`"mcp":{"transport":"streamable-http","url":"http://x/mcp","token":"mcp-secret","tokenExpiresAt":"2026-11-20T19:00:00Z"},` +
		`"model":{"baseUrl":"http://x/v1","name":"autoagents/one","token":"model-secret","weights":["quick","normal","deep"],"defaultWeight":"normal"},` +
		`"session":{"summary":null,"compactedAt":null,"pausedTurnId":null},` +
		`"config":{"heartbeatIntervalMs":0,"heartbeatTimeoutMs":45000,"maxLineBytes":65536,"runner":{}}}`

	chatPayloadJSON = `{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F41","chatId":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40","from":"human:alice","text":"Hello","sentAt":"2026-11-19T19:00:01Z"}`
)

// funcHandler builds a handler from functions; a nil function does nothing.
type funcHandler struct {
	start  func(context.Context, sdk.Welcome) error
	wake   func(context.Context, *sdk.Turn, sdk.Wake) error
	sleep  func(context.Context, sdk.SleepRequest) (string, error)
	resume func(context.Context, *sdk.Turn) error
}

func (h funcHandler) Start(ctx context.Context, w sdk.Welcome) error {
	if h.start != nil {
		return h.start(ctx, w)
	}
	return nil
}

func (h funcHandler) Wake(ctx context.Context, t *sdk.Turn, w sdk.Wake) error {
	if h.wake != nil {
		return h.wake(ctx, t, w)
	}
	return t.ProfileUpdate("Goal: test. Now: testing.")
}

func (h funcHandler) Sleep(ctx context.Context, r sdk.SleepRequest) (string, error) {
	if h.sleep != nil {
		return h.sleep(ctx, r)
	}
	return "summary", nil
}

// resumer adds Resumer to funcHandler.
type resumer struct {
	funcHandler
	paused string
}

func (r resumer) CanResume(turnID string) bool { return turnID == r.paused }
func (r resumer) Resume(ctx context.Context, t *sdk.Turn) error {
	return r.resume(ctx, t)
}

// rig is a hand-driven daemon on one end of a pipe and the SDK on the other.
type rig struct {
	t     *testing.T
	d     net.Conn
	lines chan string
	done  chan struct{} // closed when RunConn returned
	// result is what RunConn returned; read it with wait.
	result error
	id     int
}

// wait returns what RunConn returned, after it returned.
func (g *rig) wait() error {
	<-g.done
	return g.result
}

func newRig(t *testing.T, opts sdk.Options, h sdk.Handler) *rig {
	t.Helper()
	runnerSide, daemonSide := net.Pipe()
	g := &rig{t: t, d: daemonSide, lines: make(chan string, 256), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		g.result = sdk.RunConn(ctx, runnerSide, opts, h)
		close(g.done)
	}()
	go func() {
		br := bufio.NewReader(daemonSide)
		for {
			l, err := br.ReadString('\n')
			if err != nil {
				close(g.lines)
				return
			}
			g.lines <- strings.TrimSuffix(l, "\n")
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = daemonSide.Close()
		<-g.done
	})
	return g
}

func (g *rig) line() string {
	g.t.Helper()
	select {
	case l, ok := <-g.lines:
		if !ok {
			g.t.Fatal("the runner closed the connection")
		}
		return l
	case <-time.After(10 * time.Second):
		g.t.Fatal("no line from the runner within 10 seconds")
	}
	return ""
}

func (g *rig) send(s string) {
	g.t.Helper()
	if _, err := g.d.Write([]byte(s + "\n")); err != nil {
		g.t.Fatalf("write to the runner: %v", err)
	}
}

// handshake answers hello with the welcome and reads status idle.
func (g *rig) handshake(welcome string) {
	g.t.Helper()
	if l := g.line(); !strings.HasPrefix(l, `{"jsonrpc":"2.0","id":1,"method":"hello"`) {
		g.t.Fatalf("first line is not hello: %s", l)
	}
	g.send(`{"jsonrpc":"2.0","id":1,"result":` + welcome + `}`)
	if l := g.line(); l != `{"jsonrpc":"2.0","method":"status","params":{"state":"idle"}}` {
		g.t.Fatalf("want status idle after the welcome, got %s", l)
	}
}

func (g *rig) ready() {
	g.t.Helper()
	g.handshake(welcomeJSON)
}

// request sends a daemon request and returns its id.
func (g *rig) request(method, params string) int {
	g.t.Helper()
	g.id++
	g.send(`{"jsonrpc":"2.0","id":` + strconv.Itoa(g.id) + `,"method":"` + method + `","params":` + params + `}`)
	return g.id
}

// until reads lines until one contains want, and returns all of them.
func (g *rig) until(want string) []string {
	g.t.Helper()
	var out []string
	for {
		l := g.line()
		out = append(out, l)
		if strings.Contains(l, want) {
			return out
		}
	}
}

func (g *rig) wakeChat(turn string) int {
	g.t.Helper()
	return g.request("wake", `{"reason":"chat","turnId":"`+turn+`","payload":`+chatPayloadJSON+`}`)
}

func joined(lines []string) string { return strings.Join(lines, "\n") }

func echoOpts() sdk.Options {
	return sdk.Options{Runner: sdk.Info{Name: "test", Version: "0.0.1"}, Capabilities: sdk.Capabilities{Streaming: true, Cancel: true, Compaction: true}}
}
