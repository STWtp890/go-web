package client

import (
	"sync"
	"testing"
	"time"

	"gin-backend/internal/service/chat/types/message"
)

type fakeClient struct {
	mu       sync.Mutex
	sent     int
	contents []string
}

func (f *fakeClient) Close() error { return nil }
func (f *fakeClient) Send(m message.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent++
	f.contents = append(f.contents, m.Content())
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

func TestReplayQueuesLiveMessagesAfterPendingDeliveries(t *testing.T) {
	f := &fakeClient{}
	uc := NewUserChannel("1", "test-session", f)
	uc.BeginReplay()
	uc.Online()

	live := testMessage(t, "live")
	pending := testMessage(t, "pending")
	if !uc.Push(live) {
		t.Fatal("replaying channel rejected live message")
	}
	if !uc.PushReplay(pending) {
		t.Fatal("replaying channel rejected pending delivery")
	}
	if !uc.FinishReplay() {
		t.Fatal("failed to finish replay")
	}

	waitForSent(t, f, 2)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.contents) != 2 || f.contents[0] != "pending" || f.contents[1] != "live" {
		t.Fatalf("unexpected replay order: %#v", f.contents)
	}
	uc.Offline()
}

func TestReplayBufferAppliesBackpressure(t *testing.T) {
	f := &fakeClient{}
	uc := NewUserChannel("1", "test-session", f)
	uc.BeginReplay()
	uc.Online()
	m := testMessage(t, "live")
	for i := 0; i < cap(uc.msgCh); i++ {
		if !uc.Push(m) {
			t.Fatalf("replay buffer rejected message %d", i)
		}
	}
	if uc.Push(m) {
		t.Fatal("replay buffer accepted message beyond capacity")
	}
	uc.Offline()
}

func testMessage(t *testing.T, content string) message.Message {
	t.Helper()
	m, err := message.Unmarshal([]byte(`{"metadata":{"type":"text","groupType":"private","to":"2"},"content":"` + content + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func waitForSent(t *testing.T, f *fakeClient, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		sent := f.sent
		f.mu.Unlock()
		if sent == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("sent count did not reach %d", want)
}
