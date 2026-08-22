package ai_agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrEventTooLarge = errors.New("ai_agent: SSE 事件超过大小限制")

// Event 是 Agent 返回的一个 SSE 事件。Data 保留原始 JSON，由业务层按 Type 解析。
type Event struct {
	ID    string
	Type  string
	Data  json.RawMessage
	Retry time.Duration
}

func (e Event) Decode(target any) error {
	return json.Unmarshal(e.Data, target)
}

// EventStream 是一个可顺序读取、可关闭的 Agent SSE 流。
// 不应从多个 goroutine 并发调用 Next。
type EventStream struct {
	body          io.ReadCloser
	scanner       *bufio.Scanner
	maxEventBytes int
	lastEventID   string
	closeOnce     sync.Once
}

func newEventStream(body io.ReadCloser, maxEventBytes int) *EventStream {
	scanner := bufio.NewScanner(body)
	initialBufferSize := min(64*1024, maxEventBytes)
	scanner.Buffer(make([]byte, initialBufferSize), maxEventBytes)
	return &EventStream{body: body, scanner: scanner, maxEventBytes: maxEventBytes}
}

// Next 阻塞读取下一个包含 data 字段的事件。注释/心跳帧会被跳过。
func (s *EventStream) Next() (Event, error) {
	var eventType string
	var data bytes.Buffer
	var retry time.Duration
	dataFields := 0

	for s.scanner.Scan() {
		line := strings.TrimSuffix(s.scanner.Text(), "\r")
		if line == "" {
			if dataFields == 0 {
				continue
			}
			return s.event(eventType, data.Bytes(), retry), nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, found := strings.Cut(line, ":")
		if !found {
			value = ""
		} else {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			eventType = value
		case "data":
			if dataFields > 0 {
				data.WriteByte('\n')
			}
			if data.Len()+len(value) > s.maxEventBytes {
				return Event{}, ErrEventTooLarge
			}
			data.WriteString(value)
			dataFields++
		case "id":
			if !strings.ContainsRune(value, '\x00') {
				s.lastEventID = value
			}
		case "retry":
			milliseconds, err := strconv.ParseUint(value, 10, 64)
			if err == nil && milliseconds <= uint64((1<<63-1)/int64(time.Millisecond)) {
				retry = time.Duration(milliseconds) * time.Millisecond
			}
		}
	}

	if err := s.scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return Event{}, ErrEventTooLarge
		}
		return Event{}, err
	}
	if dataFields > 0 {
		return s.event(eventType, data.Bytes(), retry), nil
	}
	return Event{}, io.EOF
}

func (s *EventStream) event(eventType string, data []byte, retry time.Duration) Event {
	if eventType == "" {
		eventType = "message"
	}
	return Event{
		ID:    s.lastEventID,
		Type:  eventType,
		Data:  append(json.RawMessage(nil), data...),
		Retry: retry,
	}
}

func (s *EventStream) Close() error {
	var err error
	s.closeOnce.Do(func() { err = s.body.Close() })
	return err
}
