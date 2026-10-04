// Package serviceauth fixes the wire format of the two credential families used
// by the source-owned document and search services:
//
//  1. Service assertions: a calling service (go-web, py-agent, document-service)
//     proves who it is, which scopes it holds, and which resource subject plus
//     session context it is acting for. document-service consumes these on its
//     trusted entry points.
//  2. Capabilities: a fact source (document-service for formal documents,
//     py-agent for QQ raw content) grants a short-lived, narrowable resource
//     scope that a search service validates offline.
//
// Both use the same envelope shape as the existing mixin-search caller
// capability: base64url(payload_json) "." base64url(HMAC-SHA256(key, payload)),
// with no algorithm header, so a caller cannot negotiate or downgrade the
// algorithm. Only the boundary key holder can mint either one.
//
// This package deliberately contains no business rules: it does not know what a
// space is, which subject owns what, or which caller may claim which subject.
// Those decisions stay inside document-service.
package serviceauth
