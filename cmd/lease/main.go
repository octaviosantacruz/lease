// Command lease is the CLI entry point. Commands arrive with LEASE-005.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "lease: not implemented yet (see docs/TO_IMPLEMENT.md)")
	os.Exit(2)
}
