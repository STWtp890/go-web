package bridge

import (
	"encoding/json"
	"testing"

	"gin-backend/internal/service/chat/types/constant"
	"gin-backend/internal/service/chat/types/message"
)

func TestControlMessageCorrelatesAcceptedMessage(t *testing.T) {
	request := message.OriginMessageJson{MetaData: message.MetaData{
		ClientMessageID: "local-1",
		MessageType:     constant.TypeText,
		GroupType:       constant.GroupPrivate,
		To:              "recipient-1",
	}}
	accepted := controlMessage(request, "sender-1", constant.TypeAccepted, map[string]any{
		"messageId": uint(42),
	})
	origin := accepted.ToOrigin()
	if origin.MetaData.ClientMessageID != "local-1" || origin.MetaData.MessageType != constant.TypeAccepted {
		t.Fatalf("unexpected accepted metadata: %#v", origin.MetaData)
	}
	var payload struct {
		MessageID uint `json:"messageId"`
	}
	if err := json.Unmarshal([]byte(origin.ContentBody), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MessageID != 42 {
		t.Fatalf("unexpected accepted payload: %#v", payload)
	}
}
