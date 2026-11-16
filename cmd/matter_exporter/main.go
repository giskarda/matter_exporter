// Command matter_exporter exports the state of a Matter fabric, as seen by a
// matterjs-server, as Prometheus metrics.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	toolkit "github.com/prometheus/exporter-toolkit/web"
	webflag "github.com/prometheus/exporter-toolkit/web/kingpinflag"

	"github.com/giskarda/matter_exporter/internal/collector"
	"github.com/giskarda/matter_exporter/internal/enrich"
	"github.com/giskarda/matter_exporter/internal/matterws"
	"github.com/giskarda/matter_exporter/internal/state"
	"github.com/giskarda/matter_exporter/internal/web"
)

// defaultListenAddress uses the port reserved for this exporter's slot in
// https://github.com/prometheus/prometheus/wiki/Default-port-allocations
// (first free port at the time of writing; not yet registered there).
const defaultListenAddress = ":10063"

// version reports the module version and VCS revision recorded by the Go
// toolchain at build time.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "matter_exporter (unknown version)"
	}
	revision, modified := "unknown", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				modified = "-dirty"
			}
		}
	}
	return fmt.Sprintf("matter_exporter, version %s (revision: %s%s, go: %s)",
		info.Main.Version, revision, modified, info.GoVersion)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "matter_exporter:", err)
		os.Exit(1)
	}
}

func run() error {
	app := kingpin.New("matter_exporter", "Prometheus exporter for Matter smart-home networks.")
	app.HelpFlag.Short('h')
	app.Version(version())
	var (
		serverURL     = app.Flag("matter.server-url", "WebSocket URL of the Matter server.").Default("ws://localhost:5580/ws").String()
		roomsFile     = app.Flag("enrich.rooms-file", "Path to rooms.yaml; empty disables room enrichment.").Default("").String()
		telemetryPath = app.Flag("web.telemetry-path", "Path under which to expose metrics.").Default("/metrics").String()
		logLevel      = app.Flag("log.level", "Only log messages with the given severity or above. One of: [debug, info, warn, error]").Default("info").Enum("debug", "info", "warn", "error")
		logFormat     = app.Flag("log.format", "Output format of log messages. One of: [logfmt, json]").Default("logfmt").Enum("logfmt", "json")
		// --web.listen-address (repeatable), --web.systemd-socket, --web.config.file
		webFlags = webflag.AddFlags(app, defaultListenAddress)
	)
	if _, err := app.Parse(os.Args[1:]); err != nil {
		return err
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		return fmt.Errorf("--log.level: %w", err)
	}
	var handler slog.Handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	if *logFormat == "json" {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	logger := slog.New(handler)

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

	err := toolkit.ListenAndServe(server, webFlags, logger)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
