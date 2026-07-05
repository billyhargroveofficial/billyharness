package docsgen

import (
	"bytes"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/gateway"
)

func TestGatewayAPIReferenceCoversRoutes(t *testing.T) {
	output, err := GenerateGatewayAPI()
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range gateway.RouteDocs() {
		if !bytes.Contains(output, []byte(route.Method)) || !bytes.Contains(output, []byte(route.Pattern)) {
			t.Fatalf("gateway route %s %s missing from generated output", route.Method, route.Pattern)
		}
	}
}
