package grpcapi

import (
	"testing"

	"document-service/internal/application"

	documentv1 "packages/gen/document/v1"
)

// The boundary declares Documents next to its only caller. This assertion fails to
// compile if the business type and the transport contract drift apart, which is
// the reason the interface is declared here rather than in the application layer.
var _ Documents = (*application.Service)(nil)

func TestApplicationServiceImplementsTheBoundary(t *testing.T) {
	var service any = (*application.Service)(nil)
	if _, ok := service.(Documents); !ok {
		t.Fatal("application.Service must implement grpcapi.Documents")
	}
}

// TestEveryExposedMethodHasAPolicy is the fails-closed check: a method missing from
// the policy table is refused by the boundary at run time, but it must be caught at
// review time instead.
func TestEveryExposedMethodHasAPolicy(t *testing.T) {
	for _, method := range documentv1.DocumentService_ServiceDesc.Methods {
		fullMethod := "/" + documentv1.DocumentService_ServiceDesc.ServiceName + "/" + method.MethodName
		if _, ok := methodScopes[fullMethod]; !ok {
			t.Errorf("RPC %s has no authorization policy", fullMethod)
		}
	}
	for _, stream := range documentv1.DocumentService_ServiceDesc.Streams {
		fullMethod := "/" + documentv1.DocumentService_ServiceDesc.ServiceName + "/" + stream.StreamName
		if _, ok := methodScopes[fullMethod]; !ok {
			t.Errorf("streaming RPC %s has no authorization policy", fullMethod)
		}
	}
}

// TestPolicyTableHasNoStaleEntries catches the other direction: a policy for a
// method that no longer exists is dead configuration that hides a renamed RPC.
func TestPolicyTableHasNoStaleEntries(t *testing.T) {
	exposed := make(map[string]struct{})
	for _, method := range documentv1.DocumentService_ServiceDesc.Methods {
		exposed["/"+documentv1.DocumentService_ServiceDesc.ServiceName+"/"+method.MethodName] = struct{}{}
	}
	for _, stream := range documentv1.DocumentService_ServiceDesc.Streams {
		exposed["/"+documentv1.DocumentService_ServiceDesc.ServiceName+"/"+stream.StreamName] = struct{}{}
	}
	for fullMethod := range methodScopes {
		if _, ok := exposed[fullMethod]; !ok {
			t.Errorf("policy for %s does not match any exposed RPC", fullMethod)
		}
	}
}
