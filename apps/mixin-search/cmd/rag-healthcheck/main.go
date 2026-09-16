package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	address := flag.String("address", "127.0.0.1:9090", "mixin-search gRPC address")
	timeout := flag.Duration("timeout", 2*time.Second, "health RPC timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	connection, err := grpc.NewClient(*address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err == nil {
		defer connection.Close()
		response, callErr := healthpb.NewHealthClient(connection).Check(ctx, &healthpb.HealthCheckRequest{
			Service: mixinsearchv1.RAGService_ServiceDesc.ServiceName,
		})
		if callErr == nil && response.GetStatus() == healthpb.HealthCheckResponse_SERVING {
			return
		}
		if callErr != nil {
			err = callErr
		} else {
			err = fmt.Errorf("health status is %s", response.GetStatus())
		}
	}
	_, _ = fmt.Fprintf(os.Stderr, "mixin-search unhealthy: %v\n", err)
	os.Exit(1)
}
