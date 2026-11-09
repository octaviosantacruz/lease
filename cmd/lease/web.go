package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/octaviosantacruz/lease/internal/broker"
	"github.com/octaviosantacruz/lease/internal/store"
	"github.com/octaviosantacruz/lease/internal/web"
)

func webCommand(ctx context.Context, args []string, out, stderr io.Writer) int {
	if args[0] == "site" {
		if len(args) < 2 || args[1] != "export" {
			fmt.Fprintln(stderr, "usage: lease site export --out DIR")
			return 2
		}
		flags := flag.NewFlagSet("lease site export", flag.ContinueOnError)
		flags.SetOutput(stderr)
		dest := flags.String("out", "", "new or empty directory")
		if err := flags.Parse(args[2:]); err != nil || *dest == "" || flags.NArg() != 0 {
			return 2
		}
		if err := web.Export(*dest); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(out, "static site exported:", *dest)
		return 0
	}
	flags := flag.NewFlagSet("lease demo", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", "", "dedicated lab directory (required)")
	listen := flags.String("listen", "127.0.0.1:8789", "numeric loopback address")
	seed := flags.Bool("seed", false, "seed fictional examples only on an empty ledger")
	if err := flags.Parse(args[1:]); err != nil || *dir == "" || flags.NArg() != 0 {
		return 2
	}
	host, _, err := net.SplitHostPort(*listen)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		fmt.Fprintln(stderr, "demo requires a numeric loopback listen address")
		return 2
	}
	if err = os.MkdirAll(*dir, 0700); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	path := filepath.Join(*dir, "broker.sqlite")
	if err = store.Migrate(ctx, path); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	runtime, err := broker.Open(ctx, path, broker.DemoConfig())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer runtime.Close()
	if err = runtime.Initialize(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *seed {
		if err = broker.SeedDemo(ctx, runtime.Broker); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer listener.Close()
	app, err := web.New(runtime.Broker, listener.Addr().String())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(out, "Lease lab:", "http://"+listener.Addr().String())
	fmt.Fprintln(out, "Console:", "http://"+listener.Addr().String()+"/console/")
	return serveApp(ctx, listener, app, stderr)
}

// serveApp runs the HTTP server and the reconcile worker until ctx ends.
// Shared by the lab console and the operational server behind Ozal.
func serveApp(ctx context.Context, listener net.Listener, app *web.App, stderr io.Writer) int {
	server := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); app.Worker(workCtx) }()
	defer func() { cancel(); <-workerDone }()
	go func() {
		<-workCtx.Done()
		shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		server.Shutdown(shutdownCtx)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
