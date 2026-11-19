// SPDX-License-Identifier: MIT

package app

import (
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
)

// liveBuffer is how many events a subscriber may fall behind. A subscriber
// that falls further behind is closed; it reconnects and replays from the
// stored log, so nothing is lost.
const liveBuffer = 256

type subscriber struct {
	ch chan core.Event
}

// hub fans new events out to live subscribers. It never blocks a publisher.
type hub struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

func newHub() *hub { return &hub{subs: map[*subscriber]struct{}{}} }

func (h *hub) add() *subscriber {
	s := &subscriber{ch: make(chan core.Event, liveBuffer)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

// remove unregisters s and closes its channel, once.
func (h *hub) remove(s *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.ch)
	}
}

func (h *hub) broadcast(e core.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.ch <- e:
		default:
			delete(h.subs, s)
			close(s.ch)
		}
	}
}
