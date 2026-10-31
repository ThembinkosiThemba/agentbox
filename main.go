// Command agentbox shows and manages what coding agents run on this machine.
package main

import (
	"os"

	"agentbox/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
