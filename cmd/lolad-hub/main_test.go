package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI(t *testing.T) {
	db := filepath.Join(t.TempDir(), "hub.db")
	ctx := context.Background()
	var out, errOut bytes.Buffer
	if err := run(ctx, []string{"invite", "--note", "Ana", "--db", db}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if code := strings.TrimSpace(out.String()); !strings.HasPrefix(code, "hubinv_") || strings.Contains(errOut.String(), code) {
		t.Fatalf("invite: out %q err %q", out.String(), errOut.String())
	}
	out.Reset()
	if err := run(ctx, []string{"accounts", "--db", db}, &out, &errOut); err != nil || !strings.Contains(out.String(), "HANDLE") {
		t.Fatal(err, out.String())
	}
	// Flags after the handle are read too, not silently ignored.
	if err := run(ctx, []string{"disable", "nobody", "--db", db}, &out, &errOut); err == nil || !strings.Contains(err.Error(), `no account "nobody"`) {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"disable", "a", "b", "--db", db}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"serve", "--db", db}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "--public-url") {
		t.Fatal(err)
	}
}

func TestServeRejectsShortAdminToken(t *testing.T) {
	t.Setenv("LOLAD_HUB_ADMIN_TOKEN", "short")
	db := filepath.Join(t.TempDir(), "hub.db")
	var out, errOut bytes.Buffer
	err := run(context.Background(), []string{"serve", "--db", db, "--listen", "127.0.0.1:0", "--public-url", "http://127.0.0.1:1"}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "admin token") {
		t.Fatal(err)
	}
}
