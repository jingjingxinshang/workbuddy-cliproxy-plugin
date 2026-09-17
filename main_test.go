package main

import (
	"testing"
)

func TestRegionFallback(t *testing.T) {
	if got := regionOf(workbuddyAuth{Region: "intl"}); got != "intl" {
		t.Fatalf("regionOf(intl) = %q", got)
	}
	if got := regionOf(workbuddyAuth{Region: "unknown"}); got != "cn" {
		t.Fatalf("regionOf(unknown) = %q", got)
	}
}

func TestSplitSSE(t *testing.T) {
	chunks := splitSSE([]byte("data: {\"a\":1}\n\n\ndata: [DONE]\n\n"))
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks", len(chunks))
	}
	if string(chunks[0].Payload) != "data: {\"a\":1}\n\n" {
		t.Fatalf("unexpected first chunk: %q", chunks[0].Payload)
	}
}

func TestCatalogTags(t *testing.T) {
	if !hasTag([]string{"lite", "internal"}, "lite") {
		t.Fatal("expected lite tag")
	}
	if hasTag([]string{"chat"}, "lite") {
		t.Fatal("did not expect lite tag")
	}
}
