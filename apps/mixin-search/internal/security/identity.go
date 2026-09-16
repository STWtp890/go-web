package security

import (
	"context"
	"log/slog"
	"time"
)

type identityContextKey struct{}

// WithIdentity attaches a verified identity to the request context.
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, identity)
}

// IdentityFrom returns the verified identity of the current call.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	return identity, ok
}

// Audit outcomes recorded for every authenticated RPC.
const (
	OutcomeAllowed   = "allowed"
	OutcomeDenied    = "denied"
	OutcomeThrottled = "throttled"
)

// AuditRecord answers "who asked for what, in which scope, and what happened".
// It never contains document content or the query text.
type AuditRecord struct {
	CallerID       string
	Role           string
	UserID         string
	Method         string
	RequestedScope int
	GrantedScope   int
	Outcome        string
	Detail         string
	Duration       time.Duration
}

// AuditSink receives one record per authenticated RPC. A nil sink discards
// records; callers in this repository always install the slog sink.
type AuditSink func(AuditRecord)

// SlogAuditSink writes structured audit records through logger.
func SlogAuditSink(logger *slog.Logger) AuditSink {
	if logger == nil {
		logger = slog.Default()
	}
	return func(record AuditRecord) {
		logger.Info("mixin-search call",
			slog.String("caller", record.CallerID),
			slog.String("role", record.Role),
			slog.String("user", record.UserID),
			slog.String("method", record.Method),
			slog.Int("requested_scope", record.RequestedScope),
			slog.Int("granted_scope", record.GrantedScope),
			slog.String("outcome", record.Outcome),
			slog.String("detail", record.Detail),
			slog.Duration("duration", record.Duration),
		)
	}
}
