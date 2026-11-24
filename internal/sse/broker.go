package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Event is a typed server-sent event payload broadcast to all connected web clients.
type Event struct {
	Type      string    `json:"type"`
	Data      any       `json:"data"`
	Timestamp time.Time `json:"timestamp"`
}

// Broker manages fan-out SSE subscriptions for connected clients.
type Broker struct {
	mu      sync.RWMutex
	clients map[chan Event]struct{}
}

// NewBroker creates a new SSE event broker.
func NewBroker() *Broker {
	return &Broker{
		clients: make(map[chan Event]struct{}),
	}
}

// Subscribe registers a new client and returns its event channel and an unsubscribe function.
func (b *Broker) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.clients, ch)
			close(ch)
			b.mu.Unlock()
		})
	}
	return ch, unsub
}

// ClientCount returns the number of currently connected SSE clients.
func (b *Broker) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

// Publish broadcasts an event to all subscribed clients without blocking on slow consumers.
func (b *Broker) Publish(eventType string, data any) {
	ev := Event{
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now().UTC(),
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- ev:
		default:
			// Drop event for slow client rather than stalling the publisher.
		}
	}
}

// Notify publishes a user-facing toast notification event.
func (b *Broker) Notify(level, title, message string) {
	b.Publish("notification", map[string]string{
		"level":   level,
		"title":   title,
		"message": message,
	})
}

// Log publishes a live log line to connected clients.
func (b *Broker) Log(level, jobID, message string) {
	b.Publish("log_entry", map[string]any{
		"level":     level,
		"job_id":    jobID,
		"message":   message,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// ServeHTTP streams events to an HTTP client using Server-Sent Events (text/event-stream).
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := b.Subscribe()
	defer unsub()

	// Send initial comment to flush headers immediately.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			payload, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload)
			flusher.Flush()
		}
	}
}
