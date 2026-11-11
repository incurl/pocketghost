// Package cli implements the PocketHost command line interface with cobra. It is
// the Go replacement for the Commander based CLI and covers the same operational
// surface: serving, instance control, backups, migrations and status.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/incurl/pocketghost/internal/api"
	"github.com/incurl/pocketghost/internal/app"
	"github.com/incurl/pocketghost/internal/config"
)

// Execute runs the CLI.
func Execute() int {
	root := newRoot()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "pocketghost",
		Short:         "PocketHost control plane (Docker-free, pure Go)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       api.Version,
	}
	root.SetVersionTemplate("pocketghost {{.Version}}\n")

	root.AddCommand(
		newServeCmd(),
		newFirewallCmd(),
		newInstanceCmd(),
		newBackupCmd(),
		newStatusCmd(),
		newMigrateCmd(),
		newPocketBaseCmd(),
		newCronCmd(),
		newFTPCmd(),
		newSFTPCmd(),
	)
	return root
}

// withContext returns a context cancelled on SIGINT/SIGTERM.
func withContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// bootstrap loads configuration and builds the application graph.
func bootstrap(ctx context.Context) (*app.App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return app.New(ctx, cfg)
}
