// Package application implements the document search use cases: consuming
// document-service change events into a local index, answering queries inside an
// already-granted resource scope, and rebuilding the index from locally stored
// events.
//
// It never calls back into document-service, go-web or py-agent while answering
// a query. The consumer maintains its own cursor and tolerates duplicate and
// out-of-order delivery.
//
// This is the composition entry point; the implementation lives in the sibling
// files of this package.
package application

import (
	"context"
	"errors"

	"document-search/internal/config"
	"document-search/internal/infrastructure/postgres"

	"packages/serviceauth"
)

// Dependencies is everything the application layer needs from its composition
// root.
type Dependencies struct {
	Config config.Config
	Pool   *postgres.Pool
	Codec  *serviceauth.Codec
	// VectorIndex is the vector collection of the active index generation. It is
	// required whenever the vector flow is enabled: a service that advertises
	// hybrid search and then silently answers from the keyword index alone would
	// report a total that does not describe the index it claims to maintain.
	VectorIndex VectorIndex
}

// Service is the document search business boundary.
type Service struct {
	cfg     config.Config
	pool    *postgres.Pool
	codec   *serviceauth.Codec
	vectors VectorIndex
}

// New assembles the business boundary. It fails closed when a dependency is
// missing.
func New(dependencies Dependencies) (*Service, error) {
	if dependencies.Pool == nil {
		return nil, errors.New("document-search application: database pool is required")
	}
	if dependencies.Codec == nil {
		return nil, errors.New("document-search application: boundary codec is required")
	}
	settings, err := dependencies.Config.VectorConfig()
	if err != nil {
		return nil, err
	}
	if settings.Enabled && dependencies.VectorIndex == nil {
		return nil, errors.New("document-search application: the vector flow is enabled but no vector index was provided")
	}
	return &Service{
		cfg:     dependencies.Config,
		pool:    dependencies.Pool,
		codec:   dependencies.Codec,
		vectors: dependencies.VectorIndex,
	}, nil
}

// VectorIndex reports the vector collection the service writes and reads, or nil
// when the vector flow is disabled.
func (service *Service) VectorIndex() VectorIndex { return service.vectors }

var _ = context.Background
