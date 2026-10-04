// Package application implements the document service use cases.
//
// It owns the business rules of the formal document domain: creation and
// versioning, lifecycle transitions, knowledge spaces and membership, group to
// space binding, resource authorization, audit and the transactional Outbox. It
// depends on the domain model and on persistence/infrastructure ports only; it
// never depends on the transport layer or on the composition root.
//
// This is the composition entry point used by the transport boundary. The
// implementation lives in the sibling files of this package.
package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"document-service/internal/config"
	"document-service/internal/infrastructure/postgres"

	"github.com/google/uuid"

	"packages/serviceauth"
)

// Dependencies is everything the application layer needs from its composition
// root. Principal resolves the authenticated caller from the request context so
// the business layer can never be reached without one.
type Dependencies struct {
	Config    config.Config
	Pool      *postgres.Pool
	Principal func(context.Context) (*serviceauth.Principal, error)
	// Options are test seams: they replace the clock and the id generator so
	// transaction and revision behaviour is deterministic under test.
	Options []Option
}

// Option customizes the assembled service.
type Option func(*Service)

// WithClock replaces the clock used for timestamps.
func WithClock(clock func() time.Time) Option {
	return func(service *Service) {
		if clock != nil {
			service.now = clock
		}
	}
}

// WithIDGenerator replaces the identifier generator. A generator that returns a
// constant is only usable for a command that writes one identifier; the
// integration tests use a counter so a multi-row transaction stays inspectable.
func WithIDGenerator(generator func() string) Option {
	return func(service *Service) {
		if generator != nil {
			service.newID = generator
		}
	}
}

// WithAuditSink replaces the audit writer. It exists so a test can prove that a
// failing audit insert rolls the whole mutation back; production wiring never
// sets it and therefore always writes document_service.space_audit_events.
func WithAuditSink(sink postgres.AuditSink) Option {
	return func(service *Service) {
		if sink != nil && service.store != nil {
			service.store = service.store.WithAuditSink(sink)
		}
	}
}

// WithEventSink replaces the Outbox writer. It exists so a test can prove that a
// failing event insert rolls the whole mutation back - the business rows written
// before it included; production wiring never sets it and therefore always writes
// document_service.document_events.
func WithEventSink(sink postgres.EventSink) Option {
	return func(service *Service) {
		if sink != nil && service.store != nil {
			service.store = service.store.WithEventSink(sink)
		}
	}
}

// WithStore replaces the persistence component. It is the seam an integration
// test uses to point the same business rules at a test database.
func WithStore(store *postgres.Store) Option {
	return func(service *Service) {
		if store != nil {
			service.store = store
		}
	}
}

// Service is the document service business boundary.
type Service struct {
	cfg       config.Config
	pool      *postgres.Pool
	store     *postgres.Store
	codec     *serviceauth.Codec
	principal func(context.Context) (*serviceauth.Principal, error)
	now       func() time.Time
	newID     func() string
	capTTL    time.Duration
}

// New assembles the business boundary. It fails closed when a dependency is
// missing: a half-wired document service must not start.
func New(dependencies Dependencies) (*Service, error) {
	if dependencies.Pool == nil {
		return nil, errors.New("document-service application: database pool is required")
	}
	if dependencies.Principal == nil {
		return nil, errors.New("document-service application: principal resolver is required")
	}
	boundaryKey, err := dependencies.Config.BoundaryKey()
	if err != nil {
		return nil, err
	}
	codec, err := serviceauth.NewCodec(boundaryKey)
	if err != nil {
		return nil, err
	}
	capabilityTTL, err := dependencies.Config.CapabilityTTL()
	if err != nil {
		return nil, err
	}
	store, err := postgres.NewStore(dependencies.Pool)
	if err != nil {
		return nil, err
	}
	service := &Service{
		cfg:       dependencies.Config,
		pool:      dependencies.Pool,
		store:     store,
		codec:     codec,
		principal: dependencies.Principal,
		now:       func() time.Time { return time.Now().UTC() },
		newID:     uuid.NewString,
		capTTL:    capabilityTTL,
	}
	for _, option := range dependencies.Options {
		if option != nil {
			option(service)
		}
	}
	if service.store == nil {
		return nil, errors.New("document-service application: persistence is required")
	}
	return service, nil
}

// caller resolves the authenticated principal of the current call.
func (service *Service) caller(ctx context.Context) (*serviceauth.Principal, error) {
	if service == nil || service.principal == nil {
		return nil, fmt.Errorf("document-service application: principal resolver is not configured")
	}
	return service.principal(ctx)
}
