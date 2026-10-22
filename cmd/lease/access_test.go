package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/octaviosantacruz/lease/internal/broker"
	"github.com/octaviosantacruz/lease/internal/grant"
	"github.com/octaviosantacruz/lease/internal/store"
)

func TestLocalCLIWorkflowAndWatchCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broker.sqlite")
	config := filepath.Join(dir, "policy.json")
	body, _ := json.Marshal(broker.DemoConfig())
	if err := os.WriteFile(config, body, 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(command string, extra ...string) (int, string) {
		t.Helper()
		var out, errout bytes.Buffer
		args := append([]string{command, "--db", path, "--config", config}, extra...)
		code := run(context.Background(), args, &out, &errout)
		if code != 0 {
			t.Log(errout.String())
		}
		return code, out.String()
	}
	if code, _ := invoke("init"); code != 0 {
		t.Fatal(code)
	}
	code, output := invoke("request", "--as", "user:ines", "--entitlement", "analytics-read", "--ttl", "1m", "--reason", "INC-284")
	if code != 0 {
		t.Fatal(code)
	}
	var g store.Grant
	if err := json.Unmarshal([]byte(output), &g); err != nil {
		t.Fatal(err)
	}
	if code, _ = invoke("approve", "--as", "user:ines", "--id", g.ID); code != 1 {
		t.Fatal("self approval accepted")
	}
	if code, output = invoke("approve", "--as", "user:mateo", "--id", g.ID); code != 0 {
		t.Fatal(output)
	}
	if code, output = invoke("revoke", "--as", "user:ines", "--id", g.ID); code != 0 {
		t.Fatal(output)
	}
	if err := json.Unmarshal([]byte(output), &g); err != nil || g.State != grant.Revoked {
		t.Fatal(g, err)
	}
	for _, command := range []string{"list", "events", "reconcile"} {
		if code, _ = invoke(command); code != 0 {
			t.Fatal(command)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var out, stderr bytes.Buffer
	if code = run(ctx, []string{"run", "--db", path, "--config", config, "--interval", "100ms"}, &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if code, _ = invoke("list"); code != 0 {
		t.Fatal("worker did not release resources")
	}
}
