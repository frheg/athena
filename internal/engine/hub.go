package engine

import (
	"sync"
	"time"
)

// Event is one thing that happened in a turn, sent to clients over SSE.
type Event struct {
	ID   int    `json:"id"`
	Type string `json:"type"` // status, text, text_reset, waiting_model, recalled, tool_call, tool_result, approval, approval_answer, tainted, done, error
	Data any    `json:"data"`
}

// Hub keeps each stream's recent events, so a client that connects late or reconnects gets what it missed.
// A stream is a turn, or an owner's stream of events not tied to a turn (approvals, notifications).
type Hub struct {
	mu      sync.Mutex
	streams map[string]*stream
	keep    time.Duration
}

type stream struct {
	events []Event
	last   int
	subs   map[chan Event]struct{}
	closed bool
	at     time.Time
}

const maxEvents = 2000 // per stream; a long answer streams many small text events

// NewHub returns an empty hub. Finished turns are kept for keep, idle open streams for a day.
func NewHub(keep time.Duration) *Hub {
	h := &Hub{streams: map[string]*stream{}, keep: keep}
	go func() {
		for range time.Tick(time.Minute) {
			h.sweep()
		}
	}()
	return h
}

func (h *Hub) sweep() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, s := range h.streams {
		idle := time.Since(s.at)
		if len(s.subs) == 0 && ((s.closed && idle > h.keep) || idle > 24*time.Hour) {
			delete(h.streams, id)
		}
	}
}

func (h *Hub) get(key string) *stream {
	s := h.streams[key]
	if s == nil {
		s = &stream{subs: map[chan Event]struct{}{}, at: time.Now()}
		h.streams[key] = s
	}
	return s
}

// Publish adds an event. "done" and "error" close a turn's stream.
func (h *Hub) Publish(key, typ string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.get(key)
	s.closed = typ == "done" || typ == "error"
	s.last++
	e := Event{ID: s.last, Type: typ, Data: data}
	s.events = append(s.events, e)
	if len(s.events) > maxEvents {
		s.events = s.events[len(s.events)-maxEvents:]
	}
	s.at = time.Now()
	for c := range s.subs {
		select {
		case c <- e:
		default: // a slow client misses live events; it catches up from the stored list on reconnect
		}
	}
}

// Subscribe returns the events after `after`, a channel for new ones, and whether the stream has already closed.
func (h *Hub) Subscribe(key string, after int) ([]Event, chan Event, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.get(key)
	past := []Event{}
	for _, e := range s.events {
		if e.ID > after {
			past = append(past, e)
		}
	}
	c := make(chan Event, 512)
	s.subs[c] = struct{}{}
	return past, c, s.closed
}

// Unsubscribe stops a channel.
func (h *Hub) Unsubscribe(key string, c chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.streams[key]; s != nil {
		delete(s.subs, c)
		s.at = time.Now()
	}
}
