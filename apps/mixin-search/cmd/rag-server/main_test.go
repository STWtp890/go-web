package main

import (
	"context"
	"net"
	"testing"

	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

type testRAGService struct {
	mixinsearchv1.UnimplementedRAGServiceServer
}

func TestGRPCServerReportsHealth(t *testing.T) {
	t.Parallel()

	listener := bufconn.Listen(1024 * 1024)
	server := newGRPCServer(1024*1024, &testRAGService{})
	go func() {
		if err := server.Serve(listener); err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	connection, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	client := healthpb.NewHealthClient(connection)
	for _, service := range []string{"", mixinsearchv1.RAGService_ServiceDesc.ServiceName} {
		response, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: service})
		if err != nil {
			t.Fatalf("check health for %q: %v", service, err)
		}
		if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			t.Fatalf(
				"health status for %q = %s, want SERVING",
				service,
				response.GetStatus(),
			)
		}
	}
}
