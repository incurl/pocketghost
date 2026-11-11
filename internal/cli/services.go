package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/incurl/pocketghost/internal/app"
	"github.com/incurl/pocketghost/internal/ftp"
	"github.com/incurl/pocketghost/internal/sftp"
)

func newPocketBaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pocketbase",
		Short: "Manage the cached PocketBase binaries",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List cached PocketBase versions",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()
			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()
			for _, v := range a.Binaries.Versions() {
				fmt.Println(v)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "download <version>",
		Short: "Download and cache a PocketBase release",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()
			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()
			path, err := a.Binaries.Download(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Println(path)
			return nil
		},
	})

	return cmd
}

func newCronCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Run scheduled maintenance jobs manually",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "backup",
		Short: "Run the nightly backup sweep now",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()
			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()
			a.Scheduler.BackupAll(ctx)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "reconcile",
		Short: "Stop processes for powered-off instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()
			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()
			a.Scheduler.ReconcilePoweredOff(ctx)
			return nil
		},
	})

	return cmd
}

func newFTPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ftp",
		Short: "FTP/FTPS file service",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the FTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()
			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()

			addr := fmt.Sprintf(":%d", a.Config.FTPPort)
			srv, err := ftp.Listen(ftp.Config{
				Addr:      addr,
				PasvIP:    a.Config.FTPPasvIP,
				PasvMin:   a.Config.FTPPasvPortMin,
				PasvMax:   a.Config.FTPPasvPortMax,
				TLSConfig: a.TLS.TLSConfig(),
				Auth:      newFileAuth(a),
				Logger:    a.Logger,
			})
			if err != nil {
				return err
			}
			defer srv.Close()

			go func() {
				<-ctx.Done()
				_ = srv.Close()
			}()
			return srv.Serve()
		},
	})
	return cmd
}

func newSFTPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sftp",
		Short: "SFTP file service",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the SFTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()
			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()

			addr := fmt.Sprintf(":%d", a.Config.SFTPPort)
			srv, err := sftp.Listen(sftp.Config{
				Addr:        addr,
				HostKeyPath: a.Config.SFTPHostKey,
				Auth:        newSFTPAuth(a),
				Logger:      a.Logger,
			})
			if err != nil {
				return err
			}
			defer srv.Close()

			go func() {
				<-ctx.Done()
				_ = srv.Close()
			}()
			return srv.Serve(ctx)
		},
	})
	return cmd
}

// newSFTPAuth returns the SFTP authenticator.
func newSFTPAuth(a *app.App) *fileAuth { return &fileAuth{app: a} }

var _ = context.Background
