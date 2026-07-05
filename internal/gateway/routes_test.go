package gateway

import "testing"

func TestRouteDocsProjectRouteSpecs(t *testing.T) {
	server := &Server{}
	docs := server.RouteDocs()
	specs := server.routeSpecs()
	if len(docs) != len(specs) {
		t.Fatalf("RouteDocs length = %d, want %d", len(docs), len(specs))
	}
	seen := map[string]bool{}
	for _, doc := range docs {
		key := doc.Method + " " + doc.Pattern
		if seen[key] {
			t.Fatalf("duplicate route doc %s", key)
		}
		seen[key] = true
		if doc.Summary == "" {
			t.Fatalf("%s missing summary", key)
		}
		if doc.AuthClass == "" {
			t.Fatalf("%s missing auth class", key)
		}
	}
	for _, spec := range specs {
		key := spec.Method + " " + spec.Pattern
		if !seen[key] {
			t.Fatalf("route spec %s missing from docs", key)
		}
	}
}

func TestAuthClassForUsesGatewayPredicates(t *testing.T) {
	cases := []struct {
		method  string
		pattern string
		want    string
	}{
		{method: "GET", pattern: "/health", want: "public"},
		{method: "GET", pattern: "/ready", want: "public"},
		{method: "GET", pattern: "/v1/config", want: "local-read"},
		{method: "POST", pattern: "/v1/sessions/{id}/undo", want: "bearer-mutation"},
	}
	for _, tc := range cases {
		if got := AuthClassFor(tc.method, tc.pattern); got != tc.want {
			t.Fatalf("AuthClassFor(%s, %s) = %s, want %s", tc.method, tc.pattern, got, tc.want)
		}
	}
}
