// Command pocketghost is the PocketHost control plane: a single, CGO-free Go
// binary that replaces the Node.js orchestrator and its Docker runtime.
package main

import (
	"os"

	"github.com/incurl/pocketghost/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
