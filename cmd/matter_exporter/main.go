// Command matter_exporter exports the state of a Matter fabric, as seen by a
// matterjs-server, as Prometheus metrics.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	toolkit "github.com/prometheus/exporter-toolkit/web"

	"github.com/giskarda/matter_exporter/internal/collector"
	"github.com/giskarda/matter_exporter/internal/enrich"
	"github.com/giskarda/matter_exporter/internal/matterws"
	"github.com/giskarda/matter_exporter/internal/state"
	"github.com/giskarda/matter_exporter/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "matter_exporter:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		serverURL     = flag.String("matter.server-url", "ws://localhost:5580/ws", "WebSocket URL of the Matter server.")
		roomsFile     = flag.String("enrich.rooms-file", "", "Path to rooms.yaml; empty disables room enrichment.")
		listenAddress = flag.String("web.listen-address", ":9811", "Address to listen on for the web interface and telemetry.")
		telemetryPath = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
		webConfigFile = flag.String("web.config.file", "", "Path to an exporter-toolkit web configuration file (TLS, basic auth).")
		logLevel      = flag.String("log.level", "info", "Log level: debug, info, warn, error.")
	)
	flag.Parse()
	if flag.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", flag.Args())
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		return fmt.Errorf("--log.level: %w", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := state.New(logger)

	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collector.New(store),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	// Room enrichment is optional and lives entirely outside the core.
	var roomOf web.RoomFunc
	if *roomsFile != "" {
		enricher, err := enrich.New(*roomsFile, store, logger)
		if err != nil {
			return fmt.Errorf("--enrich.rooms-file: %w", err)
		}
		registry.MustRegister(enricher)
		roomOf = enricher.Room

		hup := make(chan os.Signal, 1)
		signal.Notify(hup, syscall.SIGHUP)
		go func() {
			for range hup {
				if err := enricher.Reload(); err != nil {
					logger.Error("reloading room mapping failed, keeping the previous one", "err", err)
				}
			}
		}()
	}

	client := &matterws.Client{URL: *serverURL, Handler: store, Logger: logger}
	go func() { _ = client.Run(ctx) }()

	mux := http.NewServeMux()
	mux.Handle(*telemetryPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.Handle("/devices", web.DevicesHandler(store, roomOf))
	mux.Handle("/-/healthy", web.HealthyHandler())
	if *telemetryPath != "/" {
		landing, err := toolkit.NewLandingPage(toolkit.LandingConfig{
			Name:        "matter_exporter",
			Description: "Prometheus exporter for Matter smart-home networks",
			Links: []toolkit.LandingLinks{
				{Address: *telemetryPath, Text: "Metrics"},
				{Address: "/devices", Text: "Devices"},
			},
		})
		if err != nil {
			return err
		}
		mux.Handle("/", landing)
	}

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	addresses := []string{*listenAddress}
	systemdSocket := false
	err := toolkit.ListenAndServe(server, &toolkit.FlagConfig{
		WebListenAddresses: &addresses,
		WebSystemdSocket:   &systemdSocket,
		WebConfigFile:      webConfigFile,
	}, logger)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
