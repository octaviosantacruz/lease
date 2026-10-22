// Command lease currently exposes explicit local database maintenance only.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/octaviosantacruz/lease/internal/store"
)

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func run(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "db" || (args[1] != "up" && args[1] != "status" && args[1] != "verify") {
		fmt.Fprintln(stderr, "usage: lease db <up|status|verify> --db PATH")
		fmt.Fprintln(stderr, "access request/approval/revocation commands are not implemented yet")
		return 2
	}
	// Un FlagSet por subcomando evita estado global y permite probar la CLI sin os.Exit.
	flags := flag.NewFlagSet("lease db "+args[1], flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("db", "", "SQLite file (required; no implicit default)")
	if err := flags.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *path == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "--db PATH is required; no positional arguments")
		return 2
	}
	switch args[1] {
	case "up":
		if err := store.Migrate(ctx, *path); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(out, "schema: 1 (up to date)")
	case "status":
		version, err := store.MigrationStatus(ctx, *path)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if version == 0 {
			fmt.Fprintln(out, "schema: 0 (migration required)")
		} else {
			fmt.Fprintln(out, "schema: 1 (up to date)")
		}
	case "verify":
		s, err := store.Open(ctx, *path)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err = s.Close(); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(out, "audit: OK (local consistency; no external anchor)")
	}
	return 0
}
