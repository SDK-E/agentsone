// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/SDK-E/agentsone/internal/sched/domain"
)

const (
	// ringSize is how many frames the hub keeps for Last-Event-ID replay.
	ringSize = 256
	// subscriberBuffer is how many frames a subscriber may fall behind before
	// the hub drops it. The client reconnects with Last-Event-ID.
	subscriberBuffer = 64
)

// Frame is one change to a task, as the task stream sends it.
type Frame struct {
	// ID increases with every frame, as 20 digits so text order is number order.
	ID   string
	Task domain.Task
}

// hub fans task changes out to subscribers and remembers the latest frames.
type hub struct {
	mu   sync.Mutex
	seq  uint64
	ring []Frame
	subs map[chan Frame]struct{}
}

// newHub starts frame numbers at start, so numbers keep increasing across
// restarts of the daemon.
func newHub(start uint64) *hub {
	return &hub{seq: start, subs: map[chan Frame]struct{}{}}
}

// publish sends the task to every subscriber. A subscriber that has fallen
// too far behind is closed instead of slowing the others.
func (h *hub) publish(t domain.Task) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	f := Frame{ID: fmt.Sprintf("%020d", h.seq), Task: t}
	if len(h.ring) == ringSize {
		copy(h.ring, h.ring[1:])
		h.ring = h.ring[:ringSize-1]
	}
	h.ring = append(h.ring, f)
	for ch := range h.subs {
		select {
		case ch <- f:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// subscribe returns a channel of frames that closes when ctx ends. A
// non-empty lastID replays the remembered frames after it first.
func (h *hub) subscribe(ctx context.Context, lastID string) <-chan Frame {
	h.mu.Lock()
	var replay []Frame
	if lastID != "" {
		for _, f := range h.ring {
			if f.ID > lastID {
				replay = append(replay, f)
			}
		}
	}
	ch := make(chan Frame, subscriberBuffer+len(replay))
	for _, f := range replay {
		ch <- f
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	go func() {
		<-ctx.Done()
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
	}()
	return ch
}
