// SPDX-License-Identifier: MIT

package conformance_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/runners/conformance"
	"github.com/SDK-E/agentsone/runners/echo"
	"github.com/SDK-E/agentsone/runners/sdk"
)

// recorder is a conformance.T that records a failure instead of failing the
// test, so the suite can be run against a runner that is broken on purpose.
type recorder struct {
	cleanups []func()
	failure  string
	skipped  bool
}

func (r *recorder) Helper()             {}
func (r *recorder) Logf(string, ...any) {}
func (r *recorder) Cleanup(f func())    { r.cleanups = append(r.cleanups, f) }
func (r *recorder) Fatalf(f string, args ...any) {
	r.failure = fmt.Sprintf(f, args...)
	runtime.Goexit()
}
func (r *recorder) Skipf(string, ...any) { r.skipped = true; runtime.Goexit() }

// runRule runs one rule against the subject and returns what it recorded.
func runRule(t *testing.T, id string, s conformance.Subject) *recorder {
	t.Helper()
	for _, rule := range conformance.Rules() {
		if rule.ID != id {
			continue
		}
		rec := &recorder{}
		done := make(chan struct{})
		go func() {
			defer close(done)
			rule.Run(rec, s)
		}()
		<-done
		for i := len(rec.cleanups) - 1; i >= 0; i-- {
			rec.cleanups[i]()
		}
		return rec
	}
	t.Fatalf("no rule %s", id)
	return nil
}

// echoBehind starts the echo runner behind a proxy that passes the daemon's
// lines through and lets filter rewrite or drop the runner's lines. A nil
// result drops the line.
func echoBehind(filter func(line []byte) []byte) conformance.Subject {
	return conformance.Subject{
		Name: "echo-behind-proxy",
		Start: conformance.InProcess(func(ctx context.Context, socket string) error {
			dir, err := os.MkdirTemp("", "agentsone-proxy")
			if err != nil {
				return fmt.Errorf("make proxy dir: %w", err)
			}
			defer func() { _ = os.RemoveAll(dir) }()
			path := filepath.Join(dir, "p.sock")
			ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if err != nil {
				return fmt.Errorf("listen: %w", err)
			}
			defer func() { _ = ln.Close() }()
			runnerDone := make(chan error, 1)
			go func() {
				opts := echo.Options()
				opts.Socket = path
				runnerDone <- sdk.Run(ctx, opts, echo.New())
			}()
			_ = ln.SetDeadline(time.Now().Add(5 * time.Second))
			runner, err := ln.Accept()
			if err != nil {
				return fmt.Errorf("accept the runner: %w", err)
			}
			daemon, err := net.Dial("unix", socket)
			if err != nil {
				return fmt.Errorf("dial the daemon: %w", err)
			}
			go func() { _, _ = io.Copy(runner, daemon) }()
			sc := bufio.NewScanner(runner)
			sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
			for sc.Scan() {
				if out := filter(append([]byte(nil), sc.Bytes()...)); out != nil {
					if _, err := daemon.Write(append(out, '\n')); err != nil {
						break
					}
				}
			}
			_ = daemon.Close()
			_ = runner.Close()
			return <-runnerDone
		}),
		Prompts: conformance.Prompts{Plain: "Hello echo", Hold: echo.CmdHold},
	}
}

func passThrough(line []byte) []byte { return line }

// TestProxyControl proves the proxy itself is harmless: the rules that the
// broken runners below fail pass through an identity filter.
func TestProxyControl(t *testing.T) {
	for _, id := range []string{"T3", "F1"} {
		rec := runRule(t, id, echoBehind(passThrough))
		if rec.failure != "" || rec.skipped {
			t.Fatalf("rule %s must pass through the plain proxy: %q", id, rec.failure)
		}
	}
}

// TestRuleT3FailsWithoutProfileUpdate: a runner that never sends profile.update
// fails T3 and the failure names the rule (TESTING.md B4c).
func TestRuleT3FailsWithoutProfileUpdate(t *testing.T) {
	rec := runRule(t, "T3", echoBehind(func(line []byte) []byte {
		if bytes.Contains(line, []byte(`"method":"profile.update"`)) {
			return nil
		}
		return line
	}))
	if !strings.Contains(rec.failure, "T3") {
		t.Fatalf("want a T3 failure, got %q", rec.failure)
	}
}

// answersBefore makes the proxied echo answer a notification: it puts an
// error response with id null in front of the runner's response with id.
func answersBefore(id string) conformance.Subject {
	ack := []byte(`{"jsonrpc":"2.0","id":` + id + `,"result":{}}`)
	return echoBehind(func(line []byte) []byte {
		if bytes.Equal(line, ack) {
			return append([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32601,"message":"Method not found"}}`+"\n"), line...)
		}
		return line
	})
}

// TestRulesK2AndK3PassThroughTheProxy proves the proxy keeps both rules
// green, so the failures below come from the answer it adds.
func TestRulesK2AndK3PassThroughTheProxy(t *testing.T) {
	for _, id := range []string{"K2", "K3"} {
		rec := runRule(t, id, echoBehind(passThrough))
		if rec.failure != "" || rec.skipped {
			t.Fatalf("rule %s must pass through the plain proxy: %q (skipped %v)", id, rec.failure, rec.skipped)
		}
	}
}

// TestRuleK2FailsWhenTheNotificationIsAnswered: in K2 the deliver after the
// credentials.update has id 2; an answer in front of it breaks the rule.
func TestRuleK2FailsWhenTheNotificationIsAnswered(t *testing.T) {
	if rec := runRule(t, "K2", answersBefore("2")); !strings.Contains(rec.failure, "K2") {
		t.Fatalf("want a K2 failure, got %q", rec.failure)
	}
}

// TestRuleK3FailsWhenTheNotificationIsAnswered: in K3 the wake after the
// credentials.update has id 1.
func TestRuleK3FailsWhenTheNotificationIsAnswered(t *testing.T) {
	if rec := runRule(t, "K3", answersBefore("1")); !strings.Contains(rec.failure, "K3") {
		t.Fatalf("want a K3 failure, got %q", rec.failure)
	}
}

// TestRuleF1FailsOnWrongJSONRPCVersion: a runner that writes jsonrpc 1.0 on
// its status lines is not schema-valid, so F1 fails.
func TestRuleF1FailsOnWrongJSONRPCVersion(t *testing.T) {
	rec := runRule(t, "F1", echoBehind(func(line []byte) []byte {
		if bytes.Contains(line, []byte(`"method":"status"`)) {
			return bytes.Replace(line, []byte(`"jsonrpc":"2.0"`), []byte(`"jsonrpc":"1.0"`), 1)
		}
		return line
	}))
	if rec.failure == "" {
		t.Fatal("F1 must fail for a runner that sends jsonrpc 1.0")
	}
}
