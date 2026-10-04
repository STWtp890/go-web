// Package application implements the QQ search use cases: applying qqsource.v1
// events from py-agent into two independent raw indexes (messages and files),
// answering queries inside an already-granted channel scope, and rebuilding from
// locally applied events.
//
// It never calls back into py-agent while answering a query, and it never treats
// raw QQ content as a formal document: promotion to a formal document is a call
// py-agent makes to document-service, after which the document belongs to
// document-service and is indexed by document-search.
//
// This is the composition entry point; the implementation lives in the sibling
// files of this package.
package application

import (
	"errors"

	"qq-search/internal/config"
	"qq-search/internal/infrastructure/postgres"

	"packages/serviceauth"
)

// Dependencies is everything the application layer needs from its composition
// root.
type Dependencies struct {
	Config config.Config
	Pool   *postgres.Pool
	Codec  *serviceauth.Codec
}

// Service is the QQ search business boundary.
type Service struct {
	cfg   config.Config
	pool  *postgres.Pool
	codec *serviceauth.Codec
}

// New assembles the business boundary. It fails closed when a dependency is
// missing.
func New(dependencies Dependencies) (*Service, error) {
	if dependencies.Pool == nil {
		return nil, errors.New("qq-search application: database pool is required")
	}
	if dependencies.Codec == nil {
		return nil, errors.New("qq-search application: boundary codec is required")
	}
	return &Service{cfg: dependencies.Config, pool: dependencies.Pool, codec: dependencies.Codec}, nil
}
