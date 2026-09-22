package main

import (
	"os"
	"testing"
)

// TestWritePreviewHTML dumps the exact document the plugin serves, so a layout
// can be eyeballed without a CPA instance. Env gated so it never runs normally.
func TestWritePreviewHTML(t *testing.T) {
	target := os.Getenv("WB_PREVIEW_OUT")
	if target == "" {
		t.Skip("set WB_PREVIEW_OUT=/tmp/wb-preview-raw.html to dump the page")
	}
	if err := os.WriteFile(target, []byte(accountsPageHTML()), 0o644); err != nil {
		t.Fatalf("write preview: %v", err)
	}
	t.Logf("wrote %s (%d bytes)", target, len(accountsPageHTML()))
}
