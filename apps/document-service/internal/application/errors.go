package application

import (
	"errors"

	"document-service/internal/domain"

	"packages/serviceauth"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errUnauthenticated is returned when a command is reached without a verified
// caller. The transport boundary normally rejects such a call before it arrives;
// the business layer refuses to guess instead of inventing a subject.
var errUnauthenticated = status.Error(codes.Unauthenticated, "document-service: an authenticated caller is required")

// grpcStatusError lets a nested call pass an already mapped status through
// unchanged, so the outermost mapping never reinterprets it.
type grpcStatusError interface {
	GRPCStatus() *status.Status
}

// grpcError maps a business error onto the status the document contract
// publishes (section 3.1). It is the single mapping point: no use case builds a
// status itself, and no caller has to match on message text.
func grpcError(err error) error {
	if err == nil {
		return nil
	}
	var withStatus grpcStatusError
	if errors.As(err, &withStatus) {
		return err
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, domain.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, domain.ErrInvalidInput):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrPrecondition):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, domain.ErrUnavailable):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, domain.ErrInvariant):
		// A violated server invariant is a defect, not a caller error. Reporting
		// INTERNAL keeps it out of the caller's retry logic.
		return status.Error(codes.Internal, err.Error())
	}
	switch {
	case errors.Is(err, serviceauth.ErrConfiguration):
		return status.Error(codes.Internal, err.Error())
	case errors.Is(err, serviceauth.ErrScopeNotGranted),
		errors.Is(err, serviceauth.ErrNamespaceViolation),
		errors.Is(err, serviceauth.ErrSubjectNotAllowed):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, serviceauth.ErrMalformedCredential),
		errors.Is(err, serviceauth.ErrSignatureMismatch),
		errors.Is(err, serviceauth.ErrUnknownClaim),
		errors.Is(err, serviceauth.ErrExpired),
		errors.Is(err, serviceauth.ErrAudienceMismatch):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, serviceauth.ErrStreamExhausted):
		// The stream reader reports exhaustion as a sentinel; the transport turns
		// it into a clean end of stream.
		return err
	}
	return status.Error(codes.Internal, err.Error())
}
