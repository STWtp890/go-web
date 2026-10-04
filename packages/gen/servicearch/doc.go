// Package servicearch holds the shared, executable boundary rules that keep the
// source-owned document and search services independent.
//
// It is a test-only helper: it is imported by each service module's architecture
// test, so one definition of "what crossing a service boundary means" serves
// every module. The rules are import-graph and SQL-text assertions, deliberately
// not file inventories.
package servicearch
