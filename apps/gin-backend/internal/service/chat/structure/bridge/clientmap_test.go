package bridge

import (
	"testing"

	"gin-backend/internal/service/chat/types/client"
	"gin-backend/internal/service/chat/types/message"
)

type revokeTestClient struct {
	closed bool
}

func (c *revokeTestClient) Send(message.Message) error { return nil }
func (c *revokeTestClient) Close() error {
	c.closed = true
	return nil
}

func TestUserClientMapRevokeSessionMatchesSID(t *testing.T) {
	m := NewUserClientMap()
	transport := &revokeTestClient{}
	uc := client.NewUserChannel("42", "old-sid", transport)
	uc.Online()
	m.Attach("42", uc)

	if got := m.RevokeSession("42", "new-sid"); got != 0 {
		t.Fatalf("RevokeSession() = %d, want 0 for a different sid", got)
	}
	if !uc.Alive() {
		t.Fatal("channel was closed for a different sid")
	}

	if got := m.RevokeSession("42", "old-sid"); got != 1 {
		t.Fatalf("RevokeSession() = %d, want 1", got)
	}
	if uc.Alive() || !transport.closed {
		t.Fatal("matching session was not closed")
	}
	if got := m.RevokeSession("42", "old-sid"); got != 0 {
		t.Fatalf("repeated RevokeSession() = %d, want 0", got)
	}
}
