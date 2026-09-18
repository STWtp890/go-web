package chat

import (
	"strings"
	"testing"
)

// TestControlSchemaKeepsItsOwnTable guards ADR-014's persistence isolation
// without a database.
//
// The chat corpus keeps its own table rather than sharing the document
// control_states table under a different namespace, because a separate table is
// what makes "independent persistence state" structural instead of a naming
// convention. The mistake that breaks it is also the easiest one to make:
// copying the document schema into this package and changing the queries but
// not the DDL. That mistake executes cleanly against PostgreSQL - both corpora
// would simply write the same row - so it is invisible until the container gate
// runs, and the container gate is exactly what cannot run when no daemon is
// available.
//
// This is a text-level guard, not a substitute for executing the schema: it
// catches the wrong table name, not a broken statement. Comments are stripped
// first, because the schema's own comment explains which table it deliberately
// does not share.
func TestControlSchemaKeepsItsOwnTable(t *testing.T) {
	const (
		tableName = "control_states"
		chatTable = "chat_" + tableName
	)
	schema := stripSQLComments(controlSchemaSQL)
	if !strings.Contains(schema, chatTable) {
		t.Fatalf("chat control schema never creates %s:\n%s", chatTable, controlSchemaSQL)
	}
	// Every mention of the shared suffix must be part of the chat table name.
	for offset := 0; ; {
		next := strings.Index(schema[offset:], tableName)
		if next < 0 {
			return
		}
		start := offset + next
		if start < len("chat_") || schema[start-len("chat_"):start] != "chat_" {
			t.Fatalf("chat control schema references %q outside %s:\n%s", tableName, chatTable, schema)
		}
		offset = start + len(tableName)
	}
}

// stripSQLComments removes line comments so prose about the table this corpus
// deliberately does not share cannot trip the check.
func stripSQLComments(schema string) string {
	lines := strings.Split(schema, "\n")
	for index, line := range lines {
		if comment := strings.Index(line, "--"); comment >= 0 {
			lines[index] = line[:comment]
		}
	}
	return strings.Join(lines, "\n")
}
