package bridge

import (
	"context"
	"sync"
	"testing"

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
	started  bool
	closed   bool
	once     sync.Once
}

func newFakeConnection() *fakeConnection {
	return &fakeConnection{incoming: make(chan []byte)}
}

func (c *fakeConnection) Start(_ func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.started = true
}

func (c *fakeConnection) Incoming() <-chan []byte    { return c.incoming }
func (c *fakeConnection) Send(message.Message) error { return nil }
func (c *fakeConnection) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		close(c.incoming)
	})
	return nil
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
