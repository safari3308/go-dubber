package sse

import (
	"testing"
	"time"
)

func TestBroker_PubSubAndUnsubscribe(t *testing.T) {
	b := NewBroker()

	ch1, unsub1 := b.Subscribe()
	ch2, unsub2 := b.Subscribe()
	defer unsub2()

	if b.ClientCount() != 2 {
		t.Fatalf("ClientCount = %d; want 2", b.ClientCount())
	}

	b.Publish("queue_state", map[string]string{"status": "running"})

	for idx, ch := range []<-chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Type != "queue_state" {
				t.Errorf("client %d got event type %q; want queue_state", idx, ev.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("client %d timed out waiting for event", idx)
		}
	}

	unsub1()
	if b.ClientCount() != 1 {
		t.Fatalf("ClientCount after unsub1 = %d; want 1", b.ClientCount())
	}
}
