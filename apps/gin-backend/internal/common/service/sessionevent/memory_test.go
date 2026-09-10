package sessionevent

import (
	"context"
	"testing"
	"time"
)

func TestMemoryBusPublishesSessionRevokedEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := NewMemoryBus()
	received := make(chan SessionRevokedEvent, 1)
	sub, err := bus.Subscribe(ctx, "test-subscriber", func(_ context.Context, event SessionRevokedEvent) error {
		received <- event
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer func() { _ = sub.Close() }()

	event := NewUserSessionRevokedEvent("42", "old-session", RevokeReasonLoginReplaced)
	if err := bus.Publish(ctx, event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	select {
	case got := <-received:
		if got != event {
			t.Fatalf("received event = %#v, want %#v", got, event)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive published event")
	}
}

func TestSessionRevokedEventValidate(t *testing.T) {
	event := NewUserSessionRevokedEvent("42", "sid", RevokeReasonLogout)
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	event.SessionID = ""
	if err := event.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error for empty session ID")
	}
}
