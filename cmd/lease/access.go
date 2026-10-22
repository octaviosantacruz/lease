package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/octaviosantacruz/lease/internal/broker"
	"github.com/octaviosantacruz/lease/internal/store"
)

func access(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("lease "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("db", "", "broker SQLite path")
	config := flags.String("config", "", "policy JSON file")
	actor := flags.String("as", "", "local operator attribution (not authentication)")
	ent := flags.String("entitlement", "", "entitlement ID")
	id := flags.String("id", "", "grant ID")
	reason := flags.String("reason", "", "justification or ticket")
	ttl := flags.Duration("ttl", 5*time.Minute, "requested access window")
	interval := flags.Duration("interval", 2*time.Second, "worker interval")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *path == "" || *config == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "--db and --config required, without positional arguments")
		return 2
	}
	c, err := broker.Load(*config)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if args[0] == "run" {
		if *interval < 100*time.Millisecond {
			fmt.Fprintln(stderr, "interval must be at least 100ms")
			return 2
		}
		ticker := time.NewTicker(*interval)
		defer ticker.Stop()
		for {
			cycle, cancel := context.WithTimeout(ctx, 15*time.Second)
			r, openErr := broker.Open(cycle, *path, c)
			if openErr != nil {
				fmt.Fprintln(stderr, openErr)
			} else {
				report, reconcileErr := r.Reconcile(cycle)
				r.Close() // libera el lock entre pasadas para permitir comandos locales
				if reconcileErr != nil {
					fmt.Fprintln(stderr, reconcileErr)
				}
				if len(report.Missing) > 0 || len(report.Drift) > 0 {
					json.NewEncoder(out).Encode(report)
				}
			}
			cancel()
			select {
			case <-ctx.Done():
				return 0
			case <-ticker.C:
			}
		}
	}
	if args[0] == "init" {
		if err = store.Migrate(ctx, *path); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	r, err := broker.Open(ctx, *path, c)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer r.Close()
	// init registra contratos, los otros comandos exigen que ya estén registrados.
	if args[0] == "init" {
		if err = r.Initialize(ctx); err == nil {
			fmt.Fprintln(out, "initialized")
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	snap, err := r.Store.Snapshot(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(snap.Entitlements) != len(c.Entitlements) {
		fmt.Fprintln(stderr, "run lease init for the configured entitlements first")
		return 1
	}
	var result any
	switch args[0] {
	case "request":
		result, err = r.Request(ctx, *ent, *actor, *reason, *ttl)
	case "approve":
		result, err = r.Approve(ctx, *id, *actor)
	case "deny":
		result, err = r.Deny(ctx, *id, *actor)
	case "revoke":
		result, err = r.Revoke(ctx, *id, *actor)
	case "list":
		result = snap.Grants
	case "events":
		result = snap.Events
	case "reconcile":
		result, err = r.Reconcile(ctx)

	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
