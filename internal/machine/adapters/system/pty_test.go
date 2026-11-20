// SPDX-License-Identifier: MIT

package system_test

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/adapters/system"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

func readUntil(t *testing.T, r io.Reader, want string) string {
	t.Helper()
	var buf bytes.Buffer
	chunk := make([]byte, 256)
	for !strings.Contains(buf.String(), want) {
		n, err := r.Read(chunk)
		buf.Write(chunk[:n])
		if err != nil {
			t.Fatalf("read %q before %q: %v", buf.String(), want, err)
		}
	}
	return buf.String()
}

func TestPtyRunsAProgram(t *testing.T) {
	spec := domain.PtySpec{Command: []string{"/bin/sh", "-c", "stty size; read line; echo got $line; exit 3"}, Cols: 120, Rows: 32}
	p, err := system.PtyStarter{}.Start(ctx(), spec, os.Getuid(), os.Getgid(), t.TempDir(), core.ProcessCell{})
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, "32 120")
	if _, err := p.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, "got hello")
	if code := p.Wait(); code != 3 {
		t.Fatalf("exit code %d", code)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("close after exit: %v", err)
	}
}

func TestPtyResizeAndHangUp(t *testing.T) {
	spec := domain.PtySpec{Command: []string{"/bin/sh", "-c", "read line; stty size; sleep 100"}, Cols: 80, Rows: 24}
	p, err := system.PtyStarter{}.Start(ctx(), spec, os.Getuid(), os.Getgid(), t.TempDir(), core.ProcessCell{})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Resize(100, 40); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write([]byte("go\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, "40 100")
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if code := p.Wait(); code == 0 {
		t.Fatal("hung-up program reports success")
	}
}

func TestPtyStartFailure(t *testing.T) {
	spec := domain.PtySpec{Command: []string{"/no/such/program"}, Cols: 80, Rows: 24}
	if _, err := (system.PtyStarter{}).Start(ctx(), spec, os.Getuid(), os.Getgid(), t.TempDir(), core.ProcessCell{}); err == nil {
		t.Fatal("missing program started")
	}
}
