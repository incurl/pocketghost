package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var scheduler bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the control plane: API, tenant proxy and scheduler",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()

			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()

			if scheduler {
				if err := a.Scheduler.Start(); err != nil {
					return err
				}
				defer a.Scheduler.Stop()
			}

			addr := fmt.Sprintf(":%d", a.Config.DaemonPort)
			srv := &http.Server{
				Addr:              addr,
				Handler:           a.DaemonHandler(),
				ReadHeaderTimeout: 15 * time.Second,
				IdleTimeout:       120 * time.Second,
			}

			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("listen %s: %w", addr, err)
			}
			a.Logger.Infof("daemon listening on %s (apex %s)", addr, a.Config.ApexDomain)

			errCh := make(chan error, 1)
			go func() { errCh <- srv.Serve(ln) }()

			select {
			case <-ctx.Done():
				a.Logger.Infof("shutting down daemon")
				shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancelShutdown()
				_ = srv.Shutdown(shutdownCtx)
				return nil
			case err := <-errCh:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			}
		},
	}
	cmd.Flags().BoolVar(&scheduler, "scheduler", true, "run scheduled maintenance jobs in-process")
	return cmd
}

func newFirewallCmd() *cobra.Command {
	var httpAddr, httpsAddr string
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: "Run the public edge: TLS termination, CORS, rate limiting and proxying",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := withContext()
			defer cancel()

			a, err := bootstrap(ctx)
			if err != nil {
				return err
			}
			defer a.Close()

			handler := a.EdgeHandler()

			httpSrv := &http.Server{Addr: httpAddr, Handler: a.TLS.HTTPHandler(handler), ReadHeaderTimeout: 15 * time.Second}
			go func() {
				a.Logger.Infof("edge HTTP listening on %s", httpAddr)
				if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					a.Logger.Errorf("edge HTTP: %v", err)
				}
			}()

			httpsSrv := &http.Server{
				Addr:              httpsAddr,
				Handler:           handler,
				TLSConfig:         a.TLS.TLSConfig(),
				ReadHeaderTimeout: 15 * time.Second,
			}
			go func() {
				a.Logger.Infof("edge HTTPS listening on %s", httpsAddr)
				if err := httpsSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
					a.Logger.Errorf("edge HTTPS: %v", err)
				}
			}()

			<-ctx.Done()
			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancelShutdown()
			_ = httpSrv.Shutdown(shutdownCtx)
			_ = httpsSrv.Shutdown(shutdownCtx)
			return nil
		},
	}
	cmd.Flags().StringVar(&httpAddr, "http", ":80", "HTTP listen address")
	cmd.Flags().StringVar(&httpsAddr, "https", ":443", "HTTPS listen address")
	return cmd
}
