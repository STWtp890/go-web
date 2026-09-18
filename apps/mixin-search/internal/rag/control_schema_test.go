package rag

import (
	"strings"
	"testing"
)

// TestControlSchemaDoesNotTouchTheChatTable is the document half of the guard
// in internal/chat: the two corpora own separate tables, so neither corpus's
// schema may create or reference the other's. See the chat-side test for why a
// wrong table name survives every test that does not talk to PostgreSQL.
func TestControlSchemaDoesNotTouchTheChatTable(t *testing.T) {
	if !strings.Contains(controlSchemaSQL, "mixin_search_control.control_states") {
		t.Fatalf("document control schema does not create its own table:\n%s", controlSchemaSQL)
	}
	if strings.Contains(controlSchemaSQL, "chat_control_states") {
		t.Fatalf("document control schema references the chat control table:\n%s", controlSchemaSQL)
	}
}
