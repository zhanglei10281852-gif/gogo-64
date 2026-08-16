// Command humpyard plans railway hump yard classification work offline. It
// reads a yard configuration and a yard order, produces a blocking plan, crest
// sequence, occupancy simulation, outbound consists, rework analysis and shift
// assignment, and persists the result in a local hash-chained store.
package main

import (
	"os"

	"HumpYard/internal/cli"
)

// main dispatches the command line and propagates the exit code.
func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
