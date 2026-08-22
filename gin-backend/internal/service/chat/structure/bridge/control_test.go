package bridge

import (
	"encoding/json"
	"testing"

	"gin-backend/internal/service/chat/types/constant"
	"gin-backend/internal/service/chat/types/message"
)

func TestControlMessageCorrelatesWebSocketAcknowledgement(t *testing.T) {
	request := message.OriginMessageJson{MetaData: message.MetaData{
		ClientMessageID: "local-1",
		MessageType:     constant.TypeText,
		GroupType:       constant.GroupPrivate,
		To:              "recipient-1",
	}}
	ack := controlMessage(request, "sender-1", constant.TypeAck, map[string]any{
		"deliveryIds": []string{"delivery-1"},
	})
	origin := ack.ToOrigin()
	if origin.MetaData.ClientMessageID != "local-1" || origin.MetaData.MessageType != constant.TypeAck {
		t.Fatalf("unexpected ACK metadata: %#v", origin.MetaData)
	}
	var payload struct {
		DeliveryIDs []string `json:"deliveryIds"`
	}
	if err := json.Unmarshal([]byte(origin.ContentBody), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.DeliveryIDs) != 1 || payload.DeliveryIDs[0] != "delivery-1" {
		t.Fatalf("unexpected ACK payload: %#v", payload)
	}
}
