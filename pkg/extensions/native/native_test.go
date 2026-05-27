package native

import (
	"context"
	"encoding/json"
	"testing"
)

// ── MakeExtensionSearchFn ─────────────────────────────────────────────────────

func TestMakeExtensionSearchFn_NilClient(t *testing.T) {
	fn := MakeExtensionSearchFn(nil)
	input, _ := json.Marshal(map[string]string{"query": "git"})
	_, err := fn(context.Background(), input)
	if err == nil {
		t.Fatal("nil client should return error")
	}
}

func TestMakeExtensionSearchFn_InvalidJSON(t *testing.T) {
	fn := MakeExtensionSearchFn(nil)
	_, err := fn(context.Background(), []byte("not-json"))
	if err == nil {
		t.Fatal("invalid JSON input should return error")
	}
}

// ── MakeExtensionInstallFn ────────────────────────────────────────────────────

func TestMakeExtensionInstallFn_NilClient(t *testing.T) {
	fn := MakeExtensionInstallFn(nil, nil, nil)
	input, _ := json.Marshal(map[string]string{"id": "some-ext"})
	_, err := fn(context.Background(), input)
	if err == nil {
		t.Fatal("nil client should return error")
	}
}

func TestMakeExtensionInstallFn_InvalidJSON(t *testing.T) {
	fn := MakeExtensionInstallFn(nil, nil, nil)
	_, err := fn(context.Background(), []byte("{bad json"))
	if err == nil {
		t.Fatal("invalid JSON input should return error")
	}
}

// ── BrowserUseTool arg validation (no browser needed) ────────────────────────

func TestBrowserUseTool_InvalidJSON(t *testing.T) {
	b := NewBrowserUseTool()
	_, err := b.Execute(context.Background(), []byte("not-json"))
	if err == nil {
		t.Fatal("invalid JSON should return error")
	}
}
