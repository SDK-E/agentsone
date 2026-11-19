// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"sync"
)

// Frame is one item on a chat or output stream of an agent. Exactly one of
// its fields is set.
type Frame struct {
	// Message is a stored chat message.
	Message *ChatFrame
	// Delta is partial reply text.
	Delta *DeltaFrame
	// Output is a chunk of runner output.
	Output *OutputFrame
	// State is an agent state change.
	State *StateFrame
}

// Stream names which stream of an agent a frame belongs to.
type Stream string

// Streams.
const (
	StreamChat   Stream = "chat"
	StreamOutput Stream = "output"
)

const subscriberBuffer = 64

type subscriber struct {
	agent  string
	stream Stream
	ch     chan Frame
}

// Hub fans frames out to live stream subscribers. A subscriber that falls
// behind by more than its buffer loses frames rather than blocking the
// society; clients reread history with listChatMessages.
type Hub struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

// NewHub returns a hub without subscribers.
func NewHub() *Hub { return &Hub{subs: map[*subscriber]struct{}{}} }

// Subscribe returns frames of agent's stream until ctx ends; the channel
// then closes.
func (h *Hub) Subscribe(ctx context.Context, agent string, stream Stream) <-chan Frame {
	sub := &subscriber{agent: agent, stream: stream, ch: make(chan Frame, subscriberBuffer)}
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	go func() {
		<-ctx.Done()
		h.mu.Lock()
		delete(h.subs, sub)
		close(sub.ch)
		h.mu.Unlock()
	}()
	return sub.ch
}

// Publish sends f to every subscriber of agent's stream.
func (h *Hub) Publish(agent string, stream Stream, f Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		if sub.agent != agent || sub.stream != stream {
			continue
		}
		select {
		case sub.ch <- f:
		default:
		}
	}
}
