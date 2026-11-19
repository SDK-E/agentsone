// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"sync"

	"github.com/SDK-E/agentsone/internal/router/domain"
)

// Change is one live list change: an entry added or changed, or an agent
// that left.
type Change struct {
	Entry   *domain.Entry
	Removed string // agent name when the agent left
}

const hubBuffer = 64

// hub fans changes out to watchers. A watcher that falls behind loses
// changes rather than blocking the daemon; its next reconcile repairs it.
type hub struct {
	mu   sync.Mutex
	subs map[chan Change]struct{}
}

func newHub() *hub { return &hub{subs: map[chan Change]struct{}{}} }

func (h *hub) subscribe(ctx context.Context) <-chan Change {
	ch := make(chan Change, hubBuffer)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	go func() {
		<-ctx.Done()
		h.mu.Lock()
		delete(h.subs, ch)
		close(ch)
		h.mu.Unlock()
	}()
	return ch
}

func (h *hub) publish(c Change) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- c:
		default:
		}
	}
}
