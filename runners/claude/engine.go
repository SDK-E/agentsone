// SPDX-License-Identifier: MIT

package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"
)

const (
	// maxEngineLine bounds one stdout line of the engine. A tool result can
	// be large; a line over this ends the engine.
	maxEngineLine = 64 << 20
	// stderrTail is how many redacted stderr lines are kept for errors.
	stderrTail = 20
	// reapWait bounds the wait for the engine after SIGKILL.
	reapWait = 2 * time.Second
)

// engine is one running Claude Code process in its own process group.
type engine struct {
	cmd   *exec.Cmd
	lines chan engineLine // closed when stdout ends
	done  chan struct{}   // closed once the process is reaped
	quit  chan struct{}   // closed by stop: stdout lines are dropped

	inMu  sync.Mutex
	stdin io.WriteCloser

	mu       sync.Mutex
	tail     []string
	initSeen bool

	stopOnce sync.Once
}

// writeFiles writes the launch files with mode 0600 in a 0700 folder. An
// existing file is replaced, never opened, so its old mode or a link in its
// place cannot expose the secret.
func writeFiles(files map[string][]byte) error {
	for path, data := range files {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("make the engine state folder: %w", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a folder needs its x bit; 0700 is the owner only
			return fmt.Errorf("close the engine state folder: %w", err)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- the runner's own state folder
		if err != nil {
			return fmt.Errorf("write %s: %w", filepath.Base(path), err)
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return fmt.Errorf("write %s: %w", filepath.Base(path), werr)
		}
	}
	return nil
}

// startEngine starts the engine binary in dir with the launch's arguments
// and environment. Every stderr line goes to log after redact.
func startEngine(path, dir string, l launch, redact func(string) string, log func(string)) (*engine, error) {
	cmd := exec.Command(path, l.Args...) // #nosec G204 -- the pinned engine binary with the runner's own arguments
	cmd.Env = l.Env
	cmd.Dir = dir
	// Its own process group, so a stop also ends whatever the engine started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("engine stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("engine stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("engine stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the engine: %w", err)
	}
	e := &engine{cmd: cmd, stdin: stdin, lines: make(chan engineLine, 256), done: make(chan struct{}), quit: make(chan struct{})}
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		e.readStdout(stdout, log)
	}()
	go func() {
		defer readers.Done()
		e.readStderr(stderr, redact, log)
	}()
	go func() {
		readers.Wait()
		_ = cmd.Wait() // the exit status is not needed: stderr says why
		close(e.done)
	}()
	return e, nil
}

func (e *engine) readStdout(r io.Reader, log func(string)) {
	defer close(e.lines)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxEngineLine)
	for sc.Scan() {
		var l engineLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil || l.Type == "" {
			log("engine: skipped a stdout line that is not stream-json")
			continue
		}
		select {
		case e.lines <- l:
		case <-e.quit:
		}
	}
	if err := sc.Err(); err != nil {
		log("engine: stdout ended: " + err.Error())
		_, _ = io.Copy(io.Discard, r) // keep the pipe drained until the engine is stopped
	}
}

func (e *engine) readStderr(r io.Reader, redact func(string) string, log func(string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4<<10), 1<<20)
	for sc.Scan() {
		line := redact(sc.Text())
		log("engine: " + line)
		e.mu.Lock()
		e.tail = append(e.tail, line)
		if len(e.tail) > stderrTail {
			e.tail = e.tail[1:]
		}
		e.mu.Unlock()
	}
	_, _ = io.Copy(io.Discard, r) // a line over 1 MiB: drop the rest
}

// stderrLines are the last stderr lines, redacted.
func (e *engine) stderrLines() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.tail)
}

// send writes one line to the engine's stdin.
func (e *engine) send(line []byte) error {
	e.inMu.Lock()
	defer e.inMu.Unlock()
	if _, err := e.stdin.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write to the engine: %w", err)
	}
	return nil
}

// exited says whether the process is gone.
func (e *engine) exited() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// waitExit waits up to d for the process to be reaped.
func (e *engine) waitExit(d time.Duration) {
	select {
	case <-e.done:
	case <-time.After(d):
	}
}

// stop closes stdin so the engine can end by itself, waits up to grace,
// then kills its whole process group. An engine left behind is a bug, so
// the group is killed even after a clean exit, for children it left.
func (e *engine) stop(grace time.Duration) {
	e.stopOnce.Do(func() {
		close(e.quit)
		e.inMu.Lock()
		_ = e.stdin.Close()
		e.inMu.Unlock()
		e.waitExit(grace)
		_ = syscall.Kill(-e.cmd.Process.Pid, syscall.SIGKILL) // ESRCH when the group is already gone
		e.waitExit(reapWait)
	})
}
