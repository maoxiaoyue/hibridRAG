package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/maoxiaoyue/hypgo/pkg/schema"
	"github.com/maoxiaoyue/hypgo/pkg/server"
	"google.golang.org/grpc"

	"agent_critic/app/controllers"
	"agent_critic/app/models"
	"agent_critic/app/services"
	"agent_critic/config"
	"agent_critic/internal/db"
	"agent_critic/internal/logger"
	"agent_critic/internal/mtls"
	"agent_critic/pb/criticpb"
)

func main() {
	cfg, err := config.Load("config/config.yaml")
	if err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		os.Exit(1)
	}

	log, err := logger.Init(cfg.Logger)
	if err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Close()

	log.Info("Starting agent_critic service...")

	database, err := db.Init(&cfg.Database)
	if err != nil {
		log.Emergencyf("Failed to initialize database: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	if cfg.Database.AutoMigrate {
		log.Info("Running database migrations...")
		if err := models.AutoMigrate(database.HypDB()); err != nil {
			log.Errorf("Failed to migrate database: %v", err)
		}
	}

	scorer := services.NewLexicalScorer(
		cfg.Critic.PassThreshold,
		cfg.Critic.Weights.Completeness,
		cfg.Critic.Weights.Grounding,
		cfg.Critic.Weights.Faithfulness,
	)
	criticSvc := services.NewCriticService(db.DB(), scorer)
	controllers.SetCriticService(criticSvc)

	srv := server.New(cfg.Hyp(), log)
	setupRoutes(srv)

	// gRPC server（CriticService）：供 agents 服務（或 orchestrator）呼叫。
	// 沿用 HTTP server 的 TLS 憑證。
	grpcAddr := cfg.GRPC.Addr
	if grpcAddr == "" {
		grpcAddr = ":8511"
	}
	// dev_pub_0.9：grpc.ca_file 非空即啟用 mTLS（RequireAndVerifyClientCert）+
	// CN 允許清單（空清單 = 拒絕全部）。
	mtlsOn := cfg.Server.TLS.Enabled && cfg.GRPC.CAFile != ""
	grpcOpts := []grpc.ServerOption{
		grpc.UnaryInterceptor(mtls.UnaryAllowlist(mtlsOn, cfg.GRPC.AllowedClientCNs, cfg.GRPC.TrustedIPs)),
		grpc.StreamInterceptor(mtls.StreamAllowlist(mtlsOn, cfg.GRPC.AllowedClientCNs, cfg.GRPC.TrustedIPs)),
	}
	if cfg.Server.TLS.Enabled {
		creds, credErr := mtls.ServerCredentials(cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile, cfg.GRPC.CAFile, len(cfg.GRPC.TrustedIPs) > 0)
		if credErr != nil {
			log.Warningf("gRPC TLS load failed (%v) — falling back to plaintext", credErr)
		} else {
			grpcOpts = append(grpcOpts, grpc.Creds(creds))
			if mtlsOn {
				log.Infof("gRPC mTLS enabled (allowed client CNs: %v)", cfg.GRPC.AllowedClientCNs)
			}
		}
	}
	grpcSrv := grpc.NewServer(grpcOpts...)
	criticpb.RegisterCriticServiceServer(grpcSrv, controllers.NewCriticGRPCServer(criticSvc))

	lis, lisErr := net.Listen("tcp", grpcAddr)
	if lisErr != nil {
		log.Warningf("gRPC listen %s failed: %v — gRPC server will not start", grpcAddr, lisErr)
	}

	serverErrors := make(chan error, 2)
	go func() {
		log.Infof("HTTP server starting on %s", cfg.Server.Addr)
		serverErrors <- srv.Start()
	}()

	if lis != nil {
		go func() {
			log.Infof("gRPC server starting on %s (CriticService)", grpcAddr)
			if err := grpcSrv.Serve(lis); err != nil {
				serverErrors <- fmt.Errorf("gRPC: %w", err)
			}
		}()
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		log.Emergencyf("Server error: %v", err)
		grpcSrv.GracefulStop()
		os.Exit(1)
	case sig := <-shutdown:
		log.Infof("Shutdown signal received: %v", sig)
		grpcSrv.GracefulStop()
		log.Info("Server stopped")
	}
}

func setupRoutes(srv *server.Server) {
	r := srv.Router()

	r.GET("/health", controllers.HealthCheck)

	api := r.NewGroup("/api")

	api.Schema(schema.Route{
		Method: "POST", Path: "/critic/evaluate",
		Summary: "評估答案品質（completeness / grounding / faithfulness），回傳評分與修訂建議",
		Tags:    []string{"critic", "evaluate"},
		Input:   models.EvaluateRequest{},
		Output:  models.CriticEvaluation{},
		Responses: map[int]schema.ResponseSchema{
			201: {Description: "evaluated"},
			400: {Description: "invalid request"},
		},
	}).Handle(controllers.Evaluate)

	api.Schema(schema.Route{
		Method: "GET", Path: "/critic/evaluations/:id",
		Summary: "取得單筆評估紀錄",
		Tags:    []string{"critic"},
		Output:  models.CriticEvaluation{},
		Params:  []schema.ParamSchema{{Name: "id", In: "path", Required: true, Type: "string"}},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"}, 404: {Description: "not found"},
		},
	}).Handle(controllers.GetEvaluation)

	api.Schema(schema.Route{
		Method: "GET", Path: "/critic/evaluations",
		Summary: "依 session 取最近的評估紀錄（revision loop 追蹤）",
		Tags:    []string{"critic"},
		Output:  []models.CriticEvaluation{},
		Params: []schema.ParamSchema{
			{Name: "session_id", In: "query", Required: true, Type: "string"},
			{Name: "limit", In: "query", Required: false, Type: "integer"},
		},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"}, 400: {Description: "missing session_id"},
		},
	}).Handle(controllers.ListEvaluations)
}
