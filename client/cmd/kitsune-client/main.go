// Command kitsune-client is the kitsune-whisper Client.
package main

import (
	"os"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
