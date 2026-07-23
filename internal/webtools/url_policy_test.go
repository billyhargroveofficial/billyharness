package webtools

import (
	"strings"
	"testing"
)

func TestAllowedHTTPSURLPrefixesAreExactAndCanonical(t *testing.T) {
	got, err := NormalizeAllowedHTTPSURLPrefixes([]string{
		"https://b.example:0443/api/",
		"https://a.example/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "https://a.example/,https://b.example/api" {
		t.Fatalf("normalized prefixes = %#v", got)
	}
	if err := ValidateURLAgainstAllowedHTTPSPrefixes(
		"https://b.example/api?cursor=next",
		got,
	); err != nil {
		t.Fatalf("exact path with query rejected: %v", err)
	}
	for _, rawURL := range []string{
		"http://b.example/api",
		"https://b.example/api/child",
		"https://b.example/api-private",
		"https://b.example/%61pi",
	} {
		if err := ValidateURLAgainstAllowedHTTPSPrefixes(rawURL, got); err == nil {
			t.Fatalf("disallowed URL accepted: %s", rawURL)
		}
	}
}

func TestAllowedHTTPSURLPrefixesRejectInvalidPolicies(t *testing.T) {
	for _, values := range [][]string{
		{" https://example.com/api"},
		{"http://example.com/api"},
		{"https://user@example.com/api"},
		{"https://example.com/api?tenant=1"},
		{"https://example.com/%61pi"},
		{"https://example.com//api"},
		{"https://example.com/api", "https://example.com/api/"},
	} {
		if _, err := NormalizeAllowedHTTPSURLPrefixes(values); err == nil {
			t.Fatalf("invalid prefixes accepted: %#v", values)
		}
	}
}
