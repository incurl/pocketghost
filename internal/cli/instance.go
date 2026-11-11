package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/incurl/pocketghost/internal/instance"
	"github.com/incurl/pocketghost/internal/logging"
)

func newInstanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instance",
		Short: "Manage tenant instances",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List instances and their native process state",
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
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSUBDOMAIN\tSTATUS\tVERSION\tPID\tPORT\tPOWER")
			for _, inst := range instances {
				pid, port := 0, 0
				if p, ok := a.Supervisor.Get(inst.ID); ok {
					pid, port = p.PID(), p.Port()
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%v\n",
					inst.ID, inst.Subdomain, inst.Status, inst.Version, pid, port, inst.Power)
			}
			return tw.Flush()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "start <instance-id>",
		Short: "Start an instance process",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(ctx context.Context, svc *instance.Service) error {
				inst, err := svc.Repo().GetInstance(ctx, args[0])
				if err != nil {
					return err
				}
				rt, err := svc.EnsureRunning(ctx, inst)
				if err != nil {
					return err
				}
				fmt.Printf("%s running pid=%d port=%d %s\n", args[0], rt.PID, rt.Port, rt.InternalURL)
				return nil
			})
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "stop <instance-id>",
		Short: "Stop an instance process",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(ctx context.Context, svc *instance.Service) error {
				if err := svc.Stop(ctx, args[0]); err != nil {
					return err
				}
				fmt.Printf("%s stopped\n", args[0])
				return nil
			})
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "restart <instance-id>",
		Short: "Restart an instance process",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(ctx context.Context, svc *instance.Service) error {
				inst, err := svc.Repo().GetInstance(ctx, args[0])
				if err != nil {
					return err
				}
				rt, err := svc.Restart(ctx, inst)
				if err != nil {
					return err
				}
				fmt.Printf("%s restarted pid=%d port=%d\n", args[0], rt.PID, rt.Port)
				return nil
			})
		},
	})

	var follow bool
	var lines int
	logsCmd := &cobra.Command{
		Use:   "logs <instance-id>",
		Short: "Show instance logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()
			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()

			if !follow {
				entries, err := a.Logs.ReadTail(args[0], "exec", lines)
				if err != nil {
					return err
				}
				for _, e := range entries {
					fmt.Printf("%s %-6s %s\n", e.Time, e.Stream, e.Message)
				}
				return nil
			}

			out := make(chan logging.Entry, 64)
			done := make(chan error, 1)
			go func() { done <- a.Logs.Follow(ctx, args[0], "exec", lines, out) }()
			for e := range out {
				fmt.Printf("%s %-6s %s\n", e.Time, e.Stream, e.Message)
			}
			return <-done
		},
	}
	logsCmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow the log file")
	logsCmd.Flags().IntVarP(&lines, "lines", "n", 200, "number of lines to show")
	cmd.AddCommand(logsCmd)

	return cmd
}
