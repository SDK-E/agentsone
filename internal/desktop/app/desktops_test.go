// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/desktop/app"
)

type accounts struct{ missing string }

func (a accounts) HumanUser(_ context.Context, human string) (app.User, error) {
	if human == a.missing {
		return app.User{}, fmt.Errorf("human %s: %w", human, core.ErrNotFound)
	}
	return app.User{Name: human, UID: 1000, GID: 1000, Home: "/home/" + human}, nil
}

// display is a desktop whose VNC server is the far end of a pipe.
type display struct {
	user    app.User
	mu      sync.Mutex
	servers []net.Conn
	dialErr error
	done    chan struct{}
	once    sync.Once
	stops   int
}

func (d *display) Dial(context.Context) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dialErr != nil {
		return nil, d.dialErr
	}
	client, server := net.Pipe()
	d.servers = append(d.servers, server)
	return client, nil
}

func (d *display) Done() <-chan struct{} { return d.done }

func (d *display) Stop() {
	d.mu.Lock()
	d.stops++
	d.mu.Unlock()
	d.crash()
}

// crash ends the display as if its compositor died.
func (d *display) crash() { d.once.Do(func() { close(d.done) }) }

type launcher struct {
	mu       sync.Mutex
	launched []*display
	fail     error
	gate     chan struct{} // when set, Launch waits for it
}

func (l *launcher) Launch(_ context.Context, u app.User) (app.Display, error) {
	if l.gate != nil {
		<-l.gate
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail != nil {
		return nil, l.fail
	}
	d := &display{user: u, done: make(chan struct{})}
	l.launched = append(l.launched, d)
	return d, nil
}

func (l *launcher) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.launched)
}

func (l *launcher) last() *display {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.launched[len(l.launched)-1]
}

func newDesktops(l *launcher, clock core.Clock, opts app.Options) *app.Desktops {
	return app.New(accounts{missing: "ghost"}, l, clock, opts)
}

var opts = app.Options{MaxDesktops: 2, IdleStop: 30 * time.Minute}

func TestConnectStartsTheHumansDesktopAsTheirOwnUser(t *testing.T) {
	l := &launcher{}
	d := newDesktops(l, coretest.NewClock(coretest.Epoch), opts)
	c, err := d.Connect(context.Background(), "ada")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	if l.count() != 1 {
		t.Fatalf("launched %d desktops, want 1", l.count())
	}
	if u := l.last().user; u.Name != "ada" || u.UID != 1000 || u.Home != "/home/ada" {
		t.Errorf("desktop runs as %+v, want ada's own user", u)
	}
}

func TestBytesFlowThroughTheConnection(t *testing.T) {
	l := &launcher{}
	d := newDesktops(l, coretest.NewClock(coretest.Epoch), opts)
	c, err := d.Connect(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	server := l.last().servers[0]
	go func() { _, _ = server.Write([]byte("RFB 003.008\n")) }()
	buf := make([]byte, 12)
	if _, err := c.Read(buf); err != nil || string(buf) != "RFB 003.008\n" {
		t.Fatalf("read %q, %v", buf, err)
	}
}

func TestASecondConnectionReusesTheRunningDesktop(t *testing.T) {
	l := &launcher{}
	d := newDesktops(l, coretest.NewClock(coretest.Epoch), opts)
	for range 2 {
		c, err := d.Connect(context.Background(), "ada")
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Close()
	}
	if l.count() != 1 {
		t.Errorf("launched %d desktops, want 1", l.count())
	}
}

func TestConcurrentFirstConnectionsStartOneDesktop(t *testing.T) {
	l := &launcher{gate: make(chan struct{})}
	d := newDesktops(l, coretest.NewClock(coretest.Epoch), opts)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Go(func() {
			c, err := d.Connect(context.Background(), "ada")
			if err == nil {
				_ = c.Close()
			}
			errs <- err
		})
	}
	close(l.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Connect: %v", err)
		}
	}
	if l.count() != 1 {
		t.Errorf("launched %d desktops, want 1", l.count())
	}
}

func TestEachHumanGetsTheirOwnDesktop(t *testing.T) {
	l := &launcher{}
	d := newDesktops(l, coretest.NewClock(coretest.Epoch), opts)
	for _, h := range []string{"ada", "bob"} {
		c, err := d.Connect(context.Background(), h)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
	}
	if l.count() != 2 || l.launched[0].user.Name != "ada" || l.launched[1].user.Name != "bob" {
		t.Errorf("launched %d desktops", l.count())
	}
}

func TestTheDesktopCapRefusesANewDesktop(t *testing.T) {
	l := &launcher{}
	d := newDesktops(l, coretest.NewClock(coretest.Epoch), app.Options{MaxDesktops: 1, IdleStop: time.Hour})
	c, err := d.Connect(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := d.Connect(context.Background(), "bob"); !errors.Is(err, app.ErrTooMany) {
		t.Fatalf("second human's desktop: %v, want ErrTooMany", err)
	}
	// ada keeps hers.
	c2, err := d.Connect(context.Background(), "ada")
	if err != nil {
		t.Fatalf("ada again: %v", err)
	}
	_ = c2.Close()
}

func TestAHumanWithoutALinuxUserGetsNoDesktop(t *testing.T) {
	l := &launcher{}
	d := newDesktops(l, coretest.NewClock(coretest.Epoch), opts)
	if _, err := d.Connect(context.Background(), "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Connect: %v, want ErrNotFound", err)
	}
	if l.count() != 0 {
		t.Errorf("launched %d desktops, want 0", l.count())
	}
}

func TestALaunchFailureIsReportedAndRetriedNextTime(t *testing.T) {
	l := &launcher{fail: errors.New("labwc: no such file")}
	d := newDesktops(l, coretest.NewClock(coretest.Epoch), opts)
	if _, err := d.Connect(context.Background(), "ada"); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("Connect: %v, want ErrUnavailable", err)
	}
	l.mu.Lock()
	l.fail = nil
	l.mu.Unlock()
	c, err := d.Connect(context.Background(), "ada")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	_ = c.Close()
}

func TestACrashedDesktopStartsAgainOnTheNextConnection(t *testing.T) {
	l := &launcher{}
	d := newDesktops(l, coretest.NewClock(coretest.Epoch), opts)
	c, err := d.Connect(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	l.last().crash()
	waitFor(t, func() bool { return len(d.Running()) == 0 })
	c, err = d.Connect(context.Background(), "ada")
	if err != nil {
		t.Fatalf("after crash: %v", err)
	}
	_ = c.Close()
	if l.count() != 2 {
		t.Errorf("launched %d desktops, want 2", l.count())
	}
}

func TestStopIdleEndsDesktopsWithoutAClientForTheIdleTime(t *testing.T) {
	l := &launcher{}
	clock := coretest.NewClock(coretest.Epoch)
	d := newDesktops(l, clock, opts)
	ada, err := d.Connect(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := d.Connect(context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bob.Close() }()
	_ = ada.Close()
	_ = ada.Close() // a second close does not count twice
	clock.Advance(29 * time.Minute)
	d.StopIdle()
	if got := d.Running(); len(got) != 2 {
		t.Fatalf("after 29 minutes running %v, want both", got)
	}
	clock.Advance(time.Minute)
	d.StopIdle()
	if got := d.Running(); len(got) != 1 || got[0] != "bob" {
		t.Fatalf("after 30 minutes running %v, want [bob] (he is still connected)", got)
	}
	if l.launched[0].stops != 1 {
		t.Errorf("ada's desktop stopped %d times, want 1", l.launched[0].stops)
	}
}

func TestReconnectingResetsTheIdleTimer(t *testing.T) {
	l := &launcher{}
	clock := coretest.NewClock(coretest.Epoch)
	d := newDesktops(l, clock, opts)
	c, err := d.Connect(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	clock.Advance(20 * time.Minute)
	c, err = d.Connect(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	clock.Advance(20 * time.Minute)
	d.StopIdle()
	if len(d.Running()) != 1 {
		t.Errorf("desktop stopped 20 minutes after its last client left")
	}
}

func TestStopAllEndsEveryDesktop(t *testing.T) {
	l := &launcher{}
	d := newDesktops(l, coretest.NewClock(coretest.Epoch), opts)
	for _, h := range []string{"ada", "bob"} {
		c, err := d.Connect(context.Background(), h)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
	}
	d.StopAll()
	if len(d.Running()) != 0 {
		t.Errorf("running %v after StopAll", d.Running())
	}
	for _, x := range l.launched {
		if x.stops != 1 {
			t.Errorf("%s's desktop stopped %d times", x.user.Name, x.stops)
		}
	}
}

func TestADialFailureIsUnavailableAndCountsNoClient(t *testing.T) {
	l := &launcher{}
	clock := coretest.NewClock(coretest.Epoch)
	d := newDesktops(l, clock, opts)
	c, err := d.Connect(context.Background(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	disp := l.last()
	disp.mu.Lock()
	disp.dialErr = errors.New("connection refused")
	disp.mu.Unlock()
	if _, err := d.Connect(context.Background(), "ada"); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("Connect: %v, want ErrUnavailable", err)
	}
	clock.Advance(30 * time.Minute)
	d.StopIdle()
	if len(d.Running()) != 0 {
		t.Errorf("a failed dial left a client counted")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5 seconds")
		}
		runtime.Gosched()
	}
}
