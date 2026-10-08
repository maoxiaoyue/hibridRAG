package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/maoxiaoyue/hypgo/pkg/schema"
	"github.com/maoxiaoyue/hypgo/pkg/server"
	"google.golang.org/grpc"

	"agent_memory/app/controllers"
	"agent_memory/app/models"
	"agent_memory/app/services"
	"agent_memory/config"
	"agent_memory/internal/cassandra"
	"agent_memory/internal/db"
	"agent_memory/internal/logger"
	"agent_memory/internal/mtls"
	"agent_memory/pb/memorypb"
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

	log.Info("Starting agent_memory service...")

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

	// Cassandra（長期記憶向量庫）— 失敗不阻擋服務啟動
	if len(cfg.Cassandra.Hosts) > 0 {
		if cdb, err := cassandra.Init(cfg.Cassandra); err != nil {
			log.Warningf("Cassandra init failed (%v) — long-term memory disabled", err)
		} else {
			log.Infof("Cassandra connected: hosts=%v keyspace=%s", cfg.Cassandra.Hosts, cfg.Cassandra.Keyspace)
			defer cassandra.Close()

			if err := cassandra.EnsureSchema(context.Background()); err != nil {
				log.Warningf("long_term_memory schema setup failed (%v) — long-term memory disabled", err)
			} else {
				ltmSvc := services.NewLongTermMemoryService(cdb.Session())
				controllers.SetLongTermMemoryService(ltmSvc)
				log.Info("LongTermMemoryService ready")
			}
		}
	}

	srv := server.New(cfg.Hyp(), log)
	setupRoutes(srv)

	// gRPC server（MemoryService）：供 agents 服務的 ContextBuilder/EpisodicWriter/
	// memory_read/memory_write 工具/IngestRunner 呼叫。沿用 HTTP server 的 TLS 憑證。
	grpcAddr := cfg.GRPC.Addr
	if grpcAddr == "" {
		grpcAddr = ":8510"
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
	memSvc := services.NewMemoryService(db.DB())
	sharedSvc := services.NewSharedMemoryService(db.DB())
	memorypb.RegisterMemoryServiceServer(grpcSrv, controllers.NewMemoryGRPCServer(memSvc, controllers.GetLongTermMemoryService(), sharedSvc))

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
			log.Infof("gRPC server starting on %s (MemoryService)", grpcAddr)
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

	// ── Personal Memory ────────────────────────────────────────────────────
	api.Schema(schema.Route{
		Method: "POST", Path: "/memory/personal",
		Summary: "建立 PersonalMemory（身份卡 + 表達 DNA）",
		Tags:    []string{"memory", "personal"},
		Input:   models.CreatePersonalMemoryRequest{},
		Output:  models.PersonalMemory{},
		Responses: map[int]schema.ResponseSchema{
			201: {Description: "created"},
			400: {Description: "invalid request"},
		},
	}).Handle(controllers.CreatePersonalMemory)

	api.Schema(schema.Route{
		Method: "GET", Path: "/memory/personal/:id",
		Summary: "取得 PersonalMemory",
		Tags:    []string{"memory", "personal"},
		Output:  models.PersonalMemory{},
		Params:  []schema.ParamSchema{{Name: "id", In: "path", Required: true, Type: "string"}},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"}, 404: {Description: "not found"},
		},
	}).Handle(controllers.GetPersonalMemory)

	api.Schema(schema.Route{
		Method: "PUT", Path: "/memory/personal/:id",
		Summary: "更新 PersonalMemory",
		Tags:    []string{"memory", "personal"},
		Input:   models.UpdatePersonalMemoryRequest{},
		Output:  models.PersonalMemory{},
		Params:  []schema.ParamSchema{{Name: "id", In: "path", Required: true, Type: "string"}},
	}).Handle(controllers.UpdatePersonalMemory)

	// ── Procedural Memory ──────────────────────────────────────────────────
	api.Schema(schema.Route{
		Method: "POST", Path: "/memory/procedural",
		Summary: "建立 ProceduralMemory（心智模型 + 啟發式）",
		Tags:    []string{"memory", "procedural"},
		Input:   models.CreateProceduralMemoryRequest{},
		Output:  models.ProceduralMemory{},
		Responses: map[int]schema.ResponseSchema{
			201: {Description: "created"},
			400: {Description: "invalid request"},
		},
	}).Handle(controllers.CreateProceduralMemory)

	api.Schema(schema.Route{
		Method: "GET", Path: "/memory/procedural/:id",
		Summary: "取得 ProceduralMemory",
		Tags:    []string{"memory", "procedural"},
		Output:  models.ProceduralMemory{},
		Params:  []schema.ParamSchema{{Name: "id", In: "path", Required: true, Type: "string"}},
	}).Handle(controllers.GetProceduralMemory)

	// ── Episodic Memory ────────────────────────────────────────────────────
	api.Schema(schema.Route{
		Method: "POST", Path: "/memory/episodic",
		Summary: "記錄對話歷史（EpisodicMemory）",
		Tags:    []string{"memory", "episodic"},
		Input:   models.CreateEpisodicMemoryRequest{},
		Output:  models.EpisodicMemory{},
		Responses: map[int]schema.ResponseSchema{
			201: {Description: "created"},
			400: {Description: "invalid request"},
		},
	}).Handle(controllers.CreateEpisodicMemory)

	api.Schema(schema.Route{
		Method: "GET", Path: "/memory/episodic/:id",
		Summary: "取得 EpisodicMemory",
		Tags:    []string{"memory", "episodic"},
		Output:  models.EpisodicMemory{},
		Params:  []schema.ParamSchema{{Name: "id", In: "path", Required: true, Type: "string"}},
	}).Handle(controllers.GetEpisodicMemory)

	// ── Tool Memory ────────────────────────────────────────────────────────
	api.Schema(schema.Route{
		Method: "POST", Path: "/memory/tool",
		Summary: "記錄工具使用（ToolMemory）",
		Tags:    []string{"memory", "tool"},
		Input:   models.CreateToolMemoryRequest{},
		Output:  models.ToolMemory{},
		Responses: map[int]schema.ResponseSchema{
			201: {Description: "created"},
			400: {Description: "invalid request"},
		},
	}).Handle(controllers.CreateToolMemory)

	api.Schema(schema.Route{
		Method: "GET", Path: "/memory/tool/:id",
		Summary: "取得 ToolMemory",
		Tags:    []string{"memory", "tool"},
		Output:  models.ToolMemory{},
		Params:  []schema.ParamSchema{{Name: "id", In: "path", Required: true, Type: "string"}},
	}).Handle(controllers.GetToolMemory)

	// ── Long-Term Memory（Cassandra ANN）────────────────────────────────────
	api.Schema(schema.Route{
		Method: "POST", Path: "/memory/long_term",
		Summary: "寫入長期記憶（需 1024 維 BGE-M3 embedding）",
		Tags:    []string{"memory", "long_term"},
		Input:   models.CreateLongTermMemoryRequest{},
		Output:  models.LongTermMemory{},
		Responses: map[int]schema.ResponseSchema{
			201: {Description: "created"},
			400: {Description: "invalid request"},
			503: {Description: "cassandra disabled"},
		},
	}).Handle(controllers.CreateLongTermMemory)

	api.Schema(schema.Route{
		Method: "GET", Path: "/memory/long_term/:id",
		Summary: "取得 LongTermMemory（不含 embedding）",
		Tags:    []string{"memory", "long_term"},
		Output:  models.LongTermMemory{},
		Params:  []schema.ParamSchema{{Name: "id", In: "path", Required: true, Type: "string"}},
	}).Handle(controllers.GetLongTermMemory)

	api.Schema(schema.Route{
		Method: "POST", Path: "/memory/long_term/search",
		Summary: "ANN 相似度搜尋（COSINE）",
		Tags:    []string{"memory", "long_term", "search"},
		Input:   models.SearchLongTermMemoryRequest{},
		Output:  []models.LongTermMemoryHit{},
	}).Handle(controllers.SearchLongTermMemory)

	api.Schema(schema.Route{
		Method: "DELETE", Path: "/memory/long_term/:id",
		Summary: "刪除一筆長期記憶",
		Tags:    []string{"memory", "long_term"},
		Params:  []schema.ParamSchema{{Name: "id", In: "path", Required: true, Type: "string"}},
	}).Handle(controllers.DeleteLongTermMemory)

	// ── Hybrid RAG Context（一鍵組裝給 LLM 用）─────────────────────────────
	api.Schema(schema.Route{
		Method: "POST", Path: "/memory/context",
		Summary: "組裝 RAG context：personal + procedural + episodic + long_term ANN",
		Tags:    []string{"memory", "context", "rag"},
		Input:   models.BuildContextRequest{},
		Output:  models.BuildContextResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			400: {Description: "missing agent_id"},
		},
	}).Handle(controllers.BuildContext)
}
