package ai_agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClientRunLifecycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer service-token" {
			t.Errorf("Authorization = %q", got)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/internal/v1/runs":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"agent-1","externalRunId":"gin-1","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/internal/v1/runs/agent-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"agent-1","status":"running"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/internal/v1/runs/agent-1/cancel":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL + "/internal", ServiceToken: "service-token"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := client.CreateRun(context.Background(), CreateRunRequest{
		ExternalRunID: "gin-1",
		Input:         "hello",
		Actor:         Actor{UserID: "42"},
	})
	if err != nil || run.ID != "agent-1" || run.Status != RunQueued {
		t.Fatalf("CreateRun() = %#v, %v", run, err)
	}
	run, err = client.GetRun(context.Background(), "agent-1")
	if err != nil || run.Status != RunRunning {
		t.Fatalf("GetRun() = %#v, %v", run, err)
	}
	if run, err = client.CancelRun(context.Background(), "agent-1"); err != nil || run != nil {
		t.Fatalf("CancelRun() = %#v, %v", run, err)
	}
}

func TestClientStreamEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") != "17" || r.Header.Get("Last-Event-ID") != "17" {
			t.Errorf("cursor query=%q header=%q", r.URL.Query().Get("after"), r.Header.Get("Last-Event-ID"))
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, ": ping\n\nid: 18\nevent: message.delta\nretry: 1500\ndata: {\"text\":\"hello\"}\n\nid: 19\nevent: message.completed\ndata: {\"messageId\":\"m-1\",\ndata: \"content\":\"done\"}\n\n")
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.StreamEvents(context.Background(), "run-1", "17")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	first, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "18" || first.Type != "message.delta" || first.Retry != 1500*time.Millisecond {
		t.Fatalf("first event = %#v", first)
	}
	var delta struct {
		Text string `json:"text"`
	}
	if err := first.Decode(&delta); err != nil || delta.Text != "hello" {
		t.Fatalf("first data = %#v, %v", delta, err)
	}

	second, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != "19" || second.Type != "message.completed" {
		t.Fatalf("second event = %#v", second)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end error = %v", err)
	}
}

func TestClientReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "agent unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetRun(context.Background(), "run-1")
	var httpError *HTTPError
	if !errors.As(err, &httpError) || !reflect.DeepEqual(httpError, &HTTPError{StatusCode: 503, Body: "agent unavailable"}) {
		t.Fatalf("error = %#v", err)
	}
}

func TestEventStreamDispatchesEmptyDataAndEnforcesLimit(t *testing.T) {
	stream := newEventStream(io.NopCloser(strings.NewReader("id: 1\ndata:\n\n")), 16)
	event, err := stream.Next()
	if err != nil || event.ID != "1" || len(event.Data) != 0 {
		t.Fatalf("empty event = %#v, %v", event, err)
	}
	_ = stream.Close()

	stream = newEventStream(io.NopCloser(strings.NewReader("data: 12345678901234567\n\n")), 16)
	if _, err := stream.Next(); !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("oversized event error = %v", err)
	}
	_ = stream.Close()
}
