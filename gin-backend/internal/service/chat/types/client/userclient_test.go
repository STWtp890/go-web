package client

import (
	"sync"
	"testing"
	"time"

	"gin-backend/internal/service/chat/types/message"
)

type fakeClient struct {
	mu   sync.Mutex
	sent int
}

func (f *fakeClient) Close() error { return nil }
func (f *fakeClient) Send(message.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent++
	return nil
}

func TestOnlineMarksChannelAliveAndDelivers(t *testing.T) {
	f := &fakeClient{}
	uc := NewUserChannel("1", "test-session", f)
	uc.Online()
	m, err := message.Unmarshal([]byte(`{"metadata":{"type":"text","groupType":"private","to":"2"},"content":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !uc.Push(m) {
		t.Fatal("online channel rejected message")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		sent := f.sent
		f.mu.Unlock()
		if sent == 1 {
			uc.Offline()
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("message was not delivered")
}
