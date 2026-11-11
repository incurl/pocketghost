package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create, list and restore instance backups",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "create <instance-id>",
		Short: "Create a backup now",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()
			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()

			name, err := a.Backups.Create(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Println(name)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list <instance-id>",
		Short: "List backups for an instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()
			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()

			names, err := a.Backups.List(args[0])
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			for _, n := range names {
				fmt.Fprintln(tw, n)
			}
			return tw.Flush()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "restore <instance-id> <backup.zip>",
		Short: "Restore an instance from a backup",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()
			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()

			if err := a.Backups.Restore(ctx, args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("restored %s from %s\n", args[0], args[1])
			return nil
		},
	})

	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon, process and instance status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()
			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()

			instances, err := a.Repo.ListInstances(ctx, "")
			if err != nil {
				return err
			}
			running := a.Supervisor.Snapshot()

			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintf(tw, "apex\t%s\n", a.Config.ApexDomain)
			fmt.Fprintf(tw, "data root\t%s\n", a.Config.DataRoot)
			fmt.Fprintf(tw, "instances\t%d\n", len(instances))
			fmt.Fprintf(tw, "processes\t%d\n", len(running))
			fmt.Fprintf(tw, "cached versions\t%v\n", a.Binaries.Versions())
			fmt.Fprintln(tw, "")
			fmt.Fprintln(tw, "INSTANCE\tPID\tPORT\tSTATUS\tSTARTED")
			for _, p := range running {
				fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\n", p.InstanceID, p.PID, p.Port, p.Status, p.StartedAt)
			}
			return tw.Flush()
		},
	}
}

func newMigrateCmd() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Import records from a legacy PocketBase mothership database",
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" {
				from = os.Getenv("MOTHERSHIP_DATA_DB")
			}
			if from == "" {
				return fmt.Errorf("--from <path to legacy data.db> is required")
			}
			ctx, cancel := withContext()
			defer cancel()
			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()

			stats, err := a.MigrateLegacy(ctx, from)
			if err != nil {
				return err
			}
			fmt.Printf("imported users=%d instances=%d edges=%d ssh_keys=%d api_tokens=%d\n",
				stats.Users, stats.Instances, stats.Edges, stats.SSHKeys, stats.APITokens)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "path to the legacy PocketBase data.db")
	return cmd
}
