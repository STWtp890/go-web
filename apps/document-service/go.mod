module document-service

go 1.26.8

// Shared contracts are consumed through versioned generated code only. Both
// replaces keep this module buildable with GOWORK=off, which is what the
// module-isolation gate checks.
require (
	packages/gen v0.0.0
	packages/serviceauth v0.0.0
)

replace packages/gen => ../../packages/gen

replace packages/serviceauth => ../../packages/serviceauth

require (
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.10.0
	google.golang.org/grpc v1.83.2
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)
