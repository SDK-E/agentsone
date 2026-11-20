// SPDX-License-Identifier: MIT

package conformance

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

// T is the part of testing.TB the suite uses, so the suite can also be run
// against a recording T to prove that it fails when a rule is broken.
type T interface {
	Helper()
	Logf(format string, args ...any)
	Fatalf(format string, args ...any)
	Skipf(format string, args ...any)
	Cleanup(func())
}

// guard is how long the suite waits for a message that must come. It turns a
// hang into a failure; a passing run never waits for it.
const guard = 15 * time.Second

// Process is a started runner.
type Process interface {
	// Exited yields the exit status once: 0 for a clean exit.
	Exited() <-chan int
	// Kill stops the runner. It is called when a rule ends.
	Kill()
}

// Subject describes the runner under test.
type Subject struct {
	// Name labels the sub-tests.
	Name string
	// Start launches the runner so that it connects to the Unix socket.
	// Use Binary or InProcess. It must not block.
	Start func(t T, socket string) Process
	// Prompts are the texts that make this runner do what the rules need.
	Prompts Prompts
	// Welcome adjusts the welcome before it is sent, for example to point the
	// model gateway at a fake one.
	Welcome func(w *WelcomeSpec)
	// SummaryInOutput says that the runner repeats the welcome's session
	// summary in its first output (rule S2). Runners that use the summary in
	// another way prove S2 in their own tests.
	SummaryInOutput bool
}

// Prompts are chat texts with a known effect on the runner.
type Prompts struct {
	// Plain starts a short turn that ends by itself and produces output.
	Plain string
	// Hold starts a turn that stays open until it is cancelled, also when a
	// message is delivered into it before its first step (rules F1 and T8
	// deliver right after status working).
	Hold string
	// Budget starts a turn whose model call is refused for budget, once.
	// Empty when the runner has no budget path (it then must declare
	// resume: false).
	Budget string
	// Long returns a chat text that makes the runner produce at least that
	// many bytes of output. Nil skips rule F2's long-output case.
	Long func(minBytes int) string
}

// Binary starts a runner executable. env is added to the process environment.
func Binary(path string, env ...string) func(T, string) Process {
	return func(t T, socket string) Process {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), path) // #nosec G204 -- the test chooses the binary
		cmd.Env = append(os.Environ(), append([]string{"AGENTSONE_RUNNER_SOCKET=" + socket}, env...)...)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start runner %s: %v", path, err)
		}
		p := &procBinary{cmd: cmd, exited: make(chan int, 1)}
		go func() {
			err := cmd.Wait()
			code := 0
			var ee *exec.ExitError
			switch {
			case errors.As(err, &ee):
				code = ee.ExitCode()
			case err != nil:
				code = -1
			}
			p.exited <- code
		}()
		return p
	}
}

type procBinary struct {
	cmd    *exec.Cmd
	exited chan int
}

func (p *procBinary) Exited() <-chan int { return p.exited }
func (p *procBinary) Kill()              { _ = p.cmd.Process.Kill() }

// InProcess starts a runner as a function in this process. run connects to the
// socket, serves, and returns; a nil return is exit status 0, an error is 1.
// run must stop when ctx ends.
func InProcess(run func(ctx context.Context, socket string) error) func(T, string) Process {
	return func(_ T, socket string) Process {
		ctx, cancel := context.WithCancel(context.Background())
		p := &procFunc{cancel: cancel, exited: make(chan int, 1), done: make(chan struct{})}
		go func() {
			defer close(p.done)
			code := 0
			if err := run(ctx, socket); err != nil && !errors.Is(err, context.Canceled) {
				code = 1
			}
			p.exited <- code
		}()
		return p
	}
}

type procFunc struct {
	cancel context.CancelFunc
	exited chan int
	done   chan struct{}
}

func (p *procFunc) Exited() <-chan int { return p.exited }

// Kill stops the runner and waits for it, so no goroutine outlives the test.
func (p *procFunc) Kill() {
	p.cancel()
	<-p.done
}
