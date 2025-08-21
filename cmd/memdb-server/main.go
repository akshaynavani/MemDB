// Command memdb-server runs the MemDB gRPC server.
//
//	memdb-server -addr :7070 -order 64 -pprof localhost:6060
//
// On SIGINT or SIGTERM it stops accepting new RPCs, waits up to
// -shutdown-timeout for in-flight ones to finish, and then exits.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/akshaynavani/MemDB/engine"
	"github.com/akshaynavani/MemDB/internal/server"
)

type config struct {
	addr            string
	pprofAddr       string
	order           int
	scanPage        int
	shutdownTimeout time.Duration
	mutexProfile    int
	blockProfile    int
	logLevel        string
	tables          string
}

func parseFlags() config {
	var c config
	flag.StringVar(&c.addr, "addr", ":7070", "gRPC listen address")
	flag.StringVar(&c.pprofAddr, "pprof", "", "if set, serve net/http/pprof on this address (e.g. localhost:6060)")
	flag.IntVar(&c.order, "order", 0, "B+ tree order for new tables (0 = default)")
	flag.IntVar(&c.scanPage, "scan-page", server.DefaultScanPageSize, "entries copied per read lock during Scan")
	flag.DurationVar(&c.shutdownTimeout, "shutdown-timeout", 10*time.Second, "time to wait for in-flight RPCs on shutdown")
	flag.IntVar(&c.mutexProfile, "mutex-profile-fraction", 0, "runtime.SetMutexProfileFraction value (0 = off); needs -pprof")
	flag.IntVar(&c.blockProfile, "block-profile-rate", 0, "runtime.SetBlockProfileRate value in ns (0 = off); needs -pprof")
	flag.StringVar(&c.logLevel, "log-level", "info", "log level: debug, info, warn, error")
	flag.StringVar(&c.tables, "tables", "", "comma-separated tables to create at startup")
	flag.Parse()
	return c
}

func main() {
	cfg := parseFlags()

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.logLevel)); err != nil {
		fmt.Fprintf(os.Stderr, "invalid -log-level %q\n", cfg.logLevel)
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if err := run(cfg, log); err != nil {
		log.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(cfg config, log *slog.Logger) error {
	eng, err := engine.New(engine.Options{Order: cfg.order})
	if err != nil {
		return err
	}
	for _, name := range splitList(cfg.tables) {
		if _, err := eng.CreateTable(name); err != nil {
			return fmt.Errorf("creating table %q: %w", name, err)
		}
	}

	lis, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(server.UnaryInterceptor(log)),
		grpc.ChainStreamInterceptor(server.StreamInterceptor(log)),
	)
	server.New(eng, server.WithScanPageSize(cfg.scanPage)).Register(grpcServer)

	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthSrv)
	reflection.Register(grpcServer)

	var pprofSrv *http.Server
	if cfg.pprofAddr != "" {
		if cfg.mutexProfile > 0 {
			runtime.SetMutexProfileFraction(cfg.mutexProfile)
		}
		if cfg.blockProfile > 0 {
			runtime.SetBlockProfileRate(cfg.blockProfile)
		}
		pprofSrv = &http.Server{
			Addr:              cfg.pprofAddr,
			Handler:           pprofMux(),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.Info("pprof listening", "addr", cfg.pprofAddr)
			if err := pprofSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("pprof server failed", "error", err)
			}
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		log.Info("memdb listening", "addr", lis.Addr().String(), "order", eng.Order())
		serveErr <- grpcServer.Serve(lis)
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down", "timeout", cfg.shutdownTimeout)
	healthSrv.Shutdown() // report NOT_SERVING to health checks while draining

	done := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		log.Info("in-flight RPCs drained")
	case <-time.After(cfg.shutdownTimeout):
		log.Warn("shutdown timeout reached, forcing stop")
		grpcServer.Stop()
	}

	if pprofSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = pprofSrv.Shutdown(shutdownCtx)
	}
	return nil
}

// pprofMux mounts the pprof handlers on a dedicated mux served only on the
// -pprof address, separate from anything else in the process.
func pprofMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
