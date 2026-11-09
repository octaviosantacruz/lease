package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/octaviosantacruz/lease/internal/broker"
	"github.com/octaviosantacruz/lease/internal/identity"
	"github.com/octaviosantacruz/lease/internal/web"
)

// serve runs the console and API behind Ozal for the configured (real)
// targets. It listens on numeric loopback only: Ozal on this host is the only
// client, and every request must carry Ozal's signed assertion. The worker
// reconciles continuously while it runs, so do not also run "lease run" on
// the same ledger.
func serve(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("lease serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("db", "", "broker SQLite path (run lease init first)")
	config := flags.String("config", "", "policy JSON file")
	identityPath := flags.String("identity", "", "Ozal identity JSON file")
	listen := flags.String("listen", "127.0.0.1:8790", "numeric loopback address Ozal proxies to")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *path == "" || *config == "" || *identityPath == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "--db, --config and --identity are required, without positional arguments")
		return 2
	}
	host, _, err := net.SplitHostPort(*listen)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		fmt.Fprintln(stderr, "serve requires a numeric loopback listen address; Ozal is the only client")
		return 2
	}
	policy, err := broker.Load(*config)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ic, err := identity.Load(*identityPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	verifier, err := identity.New(ic, os.ReadFile, time.Now)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	runtime, err := broker.Open(ctx, *path, policy)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer runtime.Close()
	snap, err := runtime.Store.Snapshot(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(snap.Entitlements) != len(policy.Entitlements) {
		fmt.Fprintln(stderr, "run lease init for the configured entitlements first")
		return 1
	}
	app, err := web.NewOperational(runtime.Broker, verifier)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer listener.Close()
	fmt.Fprintln(out, "Lease behind Ozal:", verifier.Origin(), "-> http://"+listener.Addr().String())
	return serveApp(ctx, listener, app, stderr)
}
