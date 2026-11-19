// SPDX-License-Identifier: MIT

package app

import (
	"sync"

	"github.com/SDK-E/agentsone/internal/bus/domain"
)

// liveBuffer is how many messages a subscriber may fall behind. A subscriber
// that falls further behind is closed; it reconnects and replays from the
// stored log, so nothing is lost.
const liveBuffer = 256

type subscriber struct {
	ch chan domain.Message
}

// hub fans new messages out to live subscribers. It never blocks a sender.
type hub struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

func newHub() *hub { return &hub{subs: map[*subscriber]struct{}{}} }

func (h *hub) add() *subscriber {
	s := &subscriber{ch: make(chan domain.Message, liveBuffer)}
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

func (h *hub) broadcast(m domain.Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.ch <- m:
		default:
			delete(h.subs, s)
			close(s.ch)
		}
	}
}
