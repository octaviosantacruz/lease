package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lease.sqlite")
	var out, stderr bytes.Buffer
	call := func(action string) int {
		out.Reset()
		stderr.Reset()
		return run(context.Background(), []string{"db", action, "--db", path}, &out, &stderr)
	}
	if code := call("status"); code != 0 || !strings.Contains(out.String(), "schema: 0") {
		t.Fatalf("status: code=%d %s %s", code, &out, &stderr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("status created database: %v", err)
	}
	if code := call("verify"); code != 1 {
		t.Fatalf("verify missing database: code=%d", code)
	}
	if code := call("up"); code != 0 {
		t.Fatalf("up: code=%d %s", code, &stderr)
	}
	if code := call("status"); code != 0 || !strings.Contains(out.String(), "schema: 1") {
		t.Fatalf("status after migration: code=%d %s", code, &out)
	}
	if code := call("up"); code != 0 {
		t.Fatalf("second up: code=%d %s", code, &stderr)
	}
	if code := call("verify"); code != 0 || !strings.Contains(out.String(), "audit: OK") {
		t.Fatalf("verify: code=%d %s %s", code, &out, &stderr)
	}
}

func TestCLIRequiresAnExplicitDatabase(t *testing.T) {
	for _, args := range [][]string{nil, {"request"}, {"db", "up"}, {"db", "status", "--db", ""}, {"db", "up", "--db", "file.sqlite", "extra"}} {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), args, &out, &stderr); code != 2 {
			t.Fatalf("%v: exit=%d, want 2", args, code)
		}
		if out.Len() != 0 {
			t.Fatalf("%v: printed success for invalid input", args)
		}
	}
}
