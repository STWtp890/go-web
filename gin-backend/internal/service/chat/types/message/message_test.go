package message

import "testing"

func TestUnmarshalRejectsInvalidClientMessage(t *testing.T) {
	_, err := Unmarshal([]byte(`{"metadata":{"type":"system","groupType":"private","to":"2"},"content":"x"}`))
	if err == nil {
		t.Fatal("system message must not be accepted from clients")
	}
}

func TestUnmarshalKeepsDeliveryID(t *testing.T) {
	m, err := Unmarshal([]byte(`{"metadata":{"deliveryId":"d-1","type":"text","groupType":"private","to":"2"},"content":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.ToOrigin().MetaData.DeliveryID; got != "d-1" {
		t.Fatalf("delivery ID = %q, want d-1", got)
	}
}
