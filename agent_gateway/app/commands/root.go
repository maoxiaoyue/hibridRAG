package commands

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	hyplogger "github.com/maoxiaoyue/hypgo/pkg/logger"
	"github.com/maoxiaoyue/hypgo/pkg/server"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection"

	appconfig "agent_gatway/app/config"
	"agent_gatway/app/controllers"
	"agent_gatway/app/models"
	"agent_gatway/app/routers"
	"agent_gatway/app/services"
	"agent_gatway/internal/db"
	"agent_gatway/pb/gatewaypb"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "agent_gateway",
	Short: "A2A Gateway",
	Long:  "A2A Gateway — HTTP/A2A JSON-RPC + gRPC entry point for the multi-agent architecture.",
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the gateway (HTTP + gRPC)",
	RunE:  runServe,
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	serveCmd.Flags().StringVar(&cfgFile, "config", "config/config.yaml", "config file path")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := appconfig.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log, err := initLogger(cfg)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer log.Close()

	log.Info("Starting agent_gateway service...")
	log.Infof("Registry: %s", cfg.Registry.URL)

	// ── Shared services ───────────────────────────────────────────────────────
	registryClient := services.NewRegistryClient(cfg.Registry.URL)
	grpcForwarder := services.NewGRPCForwarder(cfg.Gateway.GRPCDialTimeoutSec, services.InternalTLS{
		ClientCertFile: cfg.Internal.ClientCertFile,
		ClientKeyFile:  cfg.Internal.ClientKeyFile,
		CAFile:         cfg.Internal.CAFile,
	})
	defer grpcForwarder.Close()

	// ── HTTP / A2A server ─────────────────────────────────────────────────────
	controllers.Init(cfg)
	httpSrv := server.New(cfg.Hyp(), log)
	routers.Setup(httpSrv)

	// ── dev_pub_0.9：API Key 認證（PG api_keys 表）────────────────────────────
	var keySvc *services.APIKeyService
	if cfg.Database.DSN != "" {
		if _, dbErr := db.Init(&cfg.Database); dbErr != nil {
			if cfg.Auth.Enabled {
				return fmt.Errorf("auth.enabled but db init failed: %w", dbErr)
			}
			log.Warningf("db init failed (%v) — api key store unavailable", dbErr)
		} else {
			defer db.Close()
			migCtx, migCancel := context.WithTimeout(context.Background(), 15*time.Second)
			if migErr := models.AutoMigrateAPIKeys(migCtx, db.DB()); migErr != nil {
				migCancel()
				if cfg.Auth.Enabled {
					return fmt.Errorf("migrate api_keys: %w", migErr)
				}
				log.Warningf("migrate api_keys failed: %v", migErr)
			} else {
				migCancel()
				keySvc = services.NewAPIKeyService(db.DB())
			}
		}
	}
	if cfg.Auth.Enabled {
		if keySvc == nil {
			return fmt.Errorf("auth.enabled requires a working database.dsn (api_keys store)")
		}
		log.Info("ClientGateway API key auth enabled (one key per device)")
	} else {
		log.Warning("ClientGateway API key auth DISABLED — dev only, do not expose publicly")
	}

	// ── gRPC server：rate limit (每 IP 桶) → API Key auth，兩層都只攔 ClientGateway ─
	var rateLim *services.IPRateLimiter
	// 預設開啟；只有 auth.rate_limit.enabled 明確寫 false 才關。
	rlEnabled := cfg.Auth.RateLimit.Enabled == nil || *cfg.Auth.RateLimit.Enabled
	if rlEnabled {
		rateLim = services.NewIPRateLimiter(cfg.Auth.RateLimit.PerIPRPS, cfg.Auth.RateLimit.PerIPBurst)
		log.Infof("ClientGateway rate limit: %.0f rps / %d burst per IP",
			max(cfg.Auth.RateLimit.PerIPRPS, 60.0), max(cfg.Auth.RateLimit.PerIPBurst, 120))
	}
	grpcOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			services.RateLimitUnaryInterceptor(rateLim, cfg.Auth.TrustedIPs),
			services.APIKeyUnaryInterceptor(cfg.Auth.Enabled, keySvc, cfg.Auth.TrustedIPs),
		),
		grpc.ChainStreamInterceptor(
			services.RateLimitStreamInterceptor(rateLim, cfg.Auth.TrustedIPs),
			services.APIKeyStreamInterceptor(cfg.Auth.Enabled, keySvc, cfg.Auth.TrustedIPs),
		),
	}
	if cfg.Server.TLS.Enabled {
		creds, credErr := credentials.NewServerTLSFromFile(cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
		if credErr != nil {
			log.Warningf("gRPC TLS load failed (%v) — falling back to plaintext", credErr)
		} else {
			grpcOpts = append(grpcOpts, grpc.Creds(creds))
		}
	}
	grpcSrv := grpc.NewServer(grpcOpts...)
	gwServer := controllers.NewGRPCGatewayServer(registryClient, grpcForwarder)
	models.RegisterAgentServiceServer(grpcSrv, gwServer)

	// ── dev_pub_0.9：ClientGateway（client 全部流量的單一入口）────────────────
	clientGW, err := controllers.NewClientGatewayServer(controllers.ClientGatewayOptions{
		Version:          cfg.Gateway.Version,
		AgentsGRPCAddr:   cfg.Internal.AgentsGRPCAddr,
		GraphGRPCAddr:    cfg.Internal.GraphGRPCAddr,
		EmbeddingBaseURL: cfg.Internal.EmbeddingBaseURL,
		AgentsHTTPBase:   cfg.Internal.AgentsHTTPBase,
		ClientCertFile:   cfg.Internal.ClientCertFile,
		ClientKeyFile:    cfg.Internal.ClientKeyFile,
		CAFile:           cfg.Internal.CAFile,
		CallTimeoutSec:   cfg.Internal.CallTimeoutSec,
	})
	if err != nil {
		return fmt.Errorf("client gateway init: %w", err)
	}
	defer clientGW.Close()
	gatewaypb.RegisterClientGatewayServer(grpcSrv, clientGW)
	// gRPC reflection: lets tooling (grpcurl, Postman, insomnia) discover
	// method signatures without shipping .proto files. Method names aren't
	// secret — auth is by API key, not by method obscurity — so the
	// info leak is minimal, and observability wins outweigh it.
	reflection.Register(grpcSrv)
	log.Infof("ClientGateway ready: agents=%s graph=%s embedding=%s",
		orDash(cfg.Internal.AgentsGRPCAddr), orDash(cfg.Internal.GraphGRPCAddr), orDash(cfg.Internal.EmbeddingBaseURL))

	lis, lisErr := net.Listen("tcp", cfg.GRPC.Addr)
	if lisErr != nil {
		log.Warningf("gRPC listen %s failed: %v — gRPC server will not start", cfg.GRPC.Addr, lisErr)
	}

	// ── Start both servers ────────────────────────────────────────────────────
	serverErrors := make(chan error, 2)

	go func() {
		log.Infof("HTTP server starting on %s (A2A JSON-RPC)", cfg.Server.Addr)
		serverErrors <- httpSrv.Start()
	}()

	if lis != nil {
		go func() {
			log.Infof("gRPC server starting on %s (AgentService/Inbound)", cfg.GRPC.Addr)
			if err := grpcSrv.Serve(lis); err != nil {
				serverErrors <- fmt.Errorf("gRPC: %w", err)
			}
		}()
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		grpcSrv.GracefulStop()
		return fmt.Errorf("server error: %w", err)
	case sig := <-shutdown:
		log.Infof("Shutdown signal received: %v", sig)
		grpcSrv.GracefulStop()
		log.Info("Servers stopped")
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func initLogger(cfg *appconfig.Config) (*hyplogger.Logger, error) {
	level := cfg.Logger.Level
	if level == "" {
		level = "info"
	}
	output := cfg.Logger.Output
	if output == "" {
		output = "stdout"
	}
	return hyplogger.New(level, output, os.Stdout, cfg.Logger.Colors)
}
