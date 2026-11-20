// SPDX-License-Identifier: MIT

package desktop_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/SDK-E/agentsone/internal/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/desktop"
	"github.com/SDK-E/agentsone/internal/desktop/app"
)

type accounts struct{}

func (accounts) HumanUser(_ context.Context, human string) (app.User, error) {
	return app.User{Name: human, UID: 1000, GID: 1000, Home: "/home/" + human}, nil
}

type display struct {
	done chan struct{}
	once sync.Once
}

func (d *display) Dial(context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer func() { _ = server.Close() }()
		_, _ = server.Write([]byte("RFB 003.008\n"))
		buf := make([]byte, 1)
		_, _ = server.Read(buf)
	}()
	return client, nil
}
func (d *display) Done() <-chan struct{} { return d.done }
func (d *display) Stop()                 { d.once.Do(func() { close(d.done) }) }

type launcher struct {
	mu       sync.Mutex
	displays []*display
}

func (l *launcher) Launch(context.Context, app.User) (app.Display, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := &display{done: make(chan struct{})}
	l.displays = append(l.displays, d)
	return d, nil
}

func started(t *testing.T, config string) (*desktop.Module, *launcher) {
	t.Helper()
	deps, _ := coretest.NewDeps(t)
	cfg, err := core.ParseConfig([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	*deps.Config = cfg
	l := &launcher{}
	m := desktop.NewWith(deps, desktop.Parts{Accounts: accounts{}, Launcher: l})
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m, l
}

func TestTheModuleServesTheBridgeOutsideTheContract(t *testing.T) {
	m, l := started(t, "")
	if m.Name() != "desktop" || len(m.API().Groups) != 0 {
		t.Fatalf("name %q groups %d", m.Name(), len(m.API().Groups))
	}
	h, err := api.Compose([]core.Module{m})
	if err != nil {
		t.Fatal(err)
	}
	ada := core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(core.WithCaller(r.Context(), ada)))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/desktop/ada/vnc"
	c, res, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{"agentsone.vnc.v1"}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if res.Body != nil {
		defer func() { _ = res.Body.Close() }()
	}
	defer func() { _ = c.CloseNow() }()
	if _, msg, err := c.Read(ctx); err != nil || string(msg) != "RFB 003.008\n" {
		t.Fatalf("greeting %q %v", msg, err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-l.displays[0].done:
	default:
		t.Error("Stop left the desktop running")
	}
}

func TestBadSettingsStopTheStart(t *testing.T) {
	for _, cfg := range []string{
		"[desktop]\nmax_desktops = 0\n",
		"[desktop]\nidle_stop = \"0s\"\n",
		"[desktop]\nunknown = 1\n",
	} {
		deps, _ := coretest.NewDeps(t)
		c, err := core.ParseConfig([]byte(cfg))
		if err != nil {
			t.Fatal(err)
		}
		*deps.Config = c
		m := desktop.NewWith(deps, desktop.Parts{Accounts: accounts{}, Launcher: &launcher{}})
		if err := m.Start(context.Background()); err == nil {
			t.Errorf("%q: Start succeeded", cfg)
			_ = m.Stop(context.Background())
		}
	}
}

type brokenLauncher struct{}

func (brokenLauncher) Launch(context.Context, app.User) (app.Display, error) {
	return nil, errors.New("labwc ended at start: failed to create backend")
}

func TestADesktopThatWillNotStartIsLoggedForTheOperator(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	var buf bytes.Buffer
	deps.Log = slog.New(slog.NewTextHandler(&buf, nil))
	m := desktop.NewWith(deps, desktop.Parts{Accounts: accounts{}, Launcher: brokenLauncher{}})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()
	if _, err := m.Connect(context.Background(), "ada"); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("Connect: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "desktop did not start") || !strings.Contains(got, "failed to create backend") ||
		!strings.Contains(got, "human=ada") {
		t.Errorf("log %q", got)
	}
}

func TestBeforeStartTheBridgeAnswersUnavailable(t *testing.T) {
	deps, _ := coretest.NewDeps(t)
	m := desktop.NewWith(deps, desktop.Parts{Accounts: accounts{}, Launcher: &launcher{}})
	if _, err := m.Connect(context.Background(), "ada"); err == nil {
		t.Fatal("Connect before Start succeeded")
	}
}
