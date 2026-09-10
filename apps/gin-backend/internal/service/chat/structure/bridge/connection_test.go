package bridge

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"gin-backend/internal/service/chat/store"
	"gin-backend/internal/service/chat/types/message"
)

type emptyDeliveryStore struct{}

func (*emptyDeliveryStore) Save(context.Context, message.Message) error { return nil }
func (*emptyDeliveryStore) FetchOffline(context.Context, string, int) ([]message.TimeScaleMessage, error) {
	return nil, nil
}
func (*emptyDeliveryStore) FetchGroupHistory(context.Context, string, int) ([]message.TimeScaleMessage, error) {
	return nil, nil
}
func (*emptyDeliveryStore) DeleteByIDs(context.Context, []uint) error { return nil }
func (*emptyDeliveryStore) SaveWithDeliveries(context.Context, message.Message, []string) ([]store.Delivery, error) {
	return nil, nil
}
func (*emptyDeliveryStore) FetchPending(context.Context, string, int, store.DeliveryCursor) ([]store.Delivery, error) {
	return nil, nil
}
func (*emptyDeliveryStore) Ack(context.Context, string, []string) error { return nil }

type fakeConnection struct {
	mu       sync.Mutex
	incoming chan []byte
	sent     chan message.Message
	started  bool
	closed   bool
	once     sync.Once
}

func newFakeConnection() *fakeConnection {
	return &fakeConnection{incoming: make(chan []byte), sent: make(chan message.Message, 8)}
}

func (c *fakeConnection) Start(_ func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.started = true
}

func (c *fakeConnection) Incoming() <-chan []byte { return c.incoming }
func (c *fakeConnection) Send(m message.Message) error {
	c.sent <- m
	return nil
}
func (c *fakeConnection) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		close(c.incoming)
	})
	return nil
}

func TestOpenConnectionReturnsErrorControlForInvalidIncomingMessage(t *testing.T) {
	b := NewMessageBridge(&emptyDeliveryStore{}, nil)
	conn := newFakeConnection()
	uc := b.OpenConnection(context.Background(), "user-1", "session-1", conn)
	if uc == nil {
		t.Fatal("connection was not opened")
	}
	t.Cleanup(func() { b.Detach("user-1", uc) })

	conn.incoming <- []byte(`{"metadata":{"clientMessageId":"local-invalid-1","type":"image","groupType":"private","to":"user-2"},"content":"x"}`)

	select {
	case sent := <-conn.sent:
		origin := sent.ToOrigin()
		if origin.MetaData.MessageType != "error" {
			t.Fatalf("unexpected control type: %q", origin.MetaData.MessageType)
		}
		if origin.MetaData.ClientMessageID != "local-invalid-1" {
			t.Fatalf("clientMessageId was not preserved: %q", origin.MetaData.ClientMessageID)
		}
		if origin.MetaData.From != "server" || origin.MetaData.To != "user-1" {
			t.Fatalf("unexpected routing metadata: %#v", origin.MetaData)
		}
		var payload struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(origin.ContentBody), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Code != "INVALID_MESSAGE" || payload.Message == "" {
			t.Fatalf("unexpected error payload: %#v", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("invalid inbound message was silently dropped")
	}

	conn.incoming <- []byte(`{"metadata":`)
	select {
	case sent := <-conn.sent:
		origin := sent.ToOrigin()
		if origin.MetaData.MessageType != "error" || origin.MetaData.GroupType != "private" {
			t.Fatalf("malformed JSON did not produce a valid generic error frame: %#v", origin.MetaData)
		}
		if origin.MetaData.ClientMessageID != "" {
			t.Fatalf("malformed JSON unexpectedly produced clientMessageId: %q", origin.MetaData.ClientMessageID)
		}
	case <-time.After(time.Second):
		t.Fatal("malformed JSON was silently dropped")
	}
}

func TestOpenConnectionUsesTransportInterfaceAndRegistersClient(t *testing.T) {
	b := NewMessageBridge(&emptyDeliveryStore{}, nil)
	conn := newFakeConnection()
	uc := b.OpenConnection(context.Background(), "user-1", "session-1", conn)
	if uc == nil {
		t.Fatal("connection was not opened")
	}
	conn.mu.Lock()
	started := conn.started
	conn.mu.Unlock()
	if !started {
		t.Fatal("transport was not started")
	}
	if got := b.userClients.Get("user-1"); got != uc {
		t.Fatal("user connection was not registered")
	}

	b.Detach("user-1", uc)
	if got := b.userClients.Get("user-1"); got != nil {
		t.Fatal("detached connection remained registered")
	}
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if !closed {
		t.Fatal("detaching did not close the transport")
	}
}
