package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	memgraph "github.com/camggould/memgraph"
	"github.com/camggould/memgraph/store/postgres"
	"github.com/camggould/memgraph/store/sqlite"
	memgraphrest "github.com/camggould/memgraph-rest"
	"github.com/spf13/cobra"
)

// var (not const) so goreleaser can inject the real version via
// -ldflags="-X main.version=...".
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:     "memgraph-rest",
		Short:   "REST + SSE server for memgraph",
		Long:    "memgraph-rest exposes a memgraph store over HTTP under /v1 and streams writes over SSE at /v1/stream.",
		Version: version,
	}
	root.AddCommand(newServeCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newServeCmd() *cobra.Command {
	var (
		sqlitePath  string
		postgresDSN string
		addr        string
		corsOrigins []string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd.Context(), sqlitePath, postgresDSN, addr, corsOrigins)
		},
	}
	cmd.Flags().StringVar(&sqlitePath, "sqlite", "memgraph.db", "SQLite database path")
	cmd.Flags().StringVar(&postgresDSN, "postgres", "", "PostgreSQL DSN (overrides --sqlite if set)")
	cmd.Flags().StringVar(&addr, "addr", ":8080", "Listen address")
	cmd.Flags().StringArrayVar(&corsOrigins, "cors-origin", nil, "Allowed CORS origin; repeat for multiple")
	return cmd
}

func runServe(ctx context.Context, sqlitePath, postgresDSN, addr string, corsOrigins []string) error {
	logger := log.New(os.Stderr, "", log.LstdFlags)

	var (
		store     memgraph.Store
		storeKind string
	)
	switch {
	case postgresDSN != "":
		ps, err := postgres.OpenContext(ctx, postgresDSN)
		if err != nil {
			return fmt.Errorf("open postgres: %w", err)
		}
		store = ps
		storeKind = "postgres"
	default:
		ss, err := sqlite.Open(sqlitePath)
		if err != nil {
			return fmt.Errorf("open sqlite: %w", err)
		}
		store = ss
		storeKind = "sqlite"
	}
	defer store.Close()

	token := os.Getenv("MEMGRAPH_HTTP_TOKEN")

	opts := []memgraphrest.Option{
		memgraphrest.WithLogger(logger),
		memgraphrest.WithVersion(version),
		memgraphrest.WithStoreKind(storeKind),
	}
	if token != "" {
		opts = append(opts, memgraphrest.WithToken(token))
	}
	if len(corsOrigins) > 0 {
		opts = append(opts, memgraphrest.WithCORS(corsOrigins...))
	}

	srv := memgraphrest.New(store, opts...)
	defer srv.Close()

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	authState := "disabled"
	if srv.AuthEnabled() {
		authState = "enabled"
	}
	corsState := "disabled"
	if srv.CORSEnabled() {
		corsState = "enabled"
	}
	fmt.Printf("memgraph-rest listening on %s (auth: %s, cors: %s)\n", addr, authState, corsState)

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-sigCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
