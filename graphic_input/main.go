// graphic_input — 知識圖譜人工輸入服務（內網 dev_090 專用）。
//
//   - REST :8505（dev_090 client「Graph Input」分頁）
//   - gRPC :8513（內網 CLI/腳本）
//   - 下游只有 agent_graph（硬依賴——本服務存在意義就是寫圖譜）
//   - 文件雙路徑：Obsidian（vault inbox/ 筆記 → agent_graph 同步管線）
//     與 PG（agent_graph IngestText 直接抽取進圖）
//
// dev_pub_0.9 外網版不部署此服務；gateway 不轉發任何對應 RPC。
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maoxiaoyue/hypgo/pkg/schema"
	"github.com/maoxiaoyue/hypgo/pkg/server"
	"google.golang.org/grpc"

	"graphic_input/app/controllers"
	"graphic_input/app/models"
	"graphic_input/app/services"
	"graphic_input/config"
	"graphic_input/internal/db"
	"graphic_input/internal/logger"
	"graphic_input/internal/mtls"
	"graphic_input/pb/graphic_inputpb"
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

	log.Info("Starting graphic_input service...")

	// ── PG（graphic_input_log）────────────────────────────────────────────────
	if _, err := db.Init(&cfg.Database); err != nil {
		log.Emergencyf("Failed to initialize database: %v", err)
		os.Exit(1)
	}
	defer db.Close()
	if cfg.Database.AutoMigrate {
		migCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := models.AutoMigrate(migCtx, db.DB()); err != nil {
			cancel()
			log.Emergencyf("Failed to migrate graphic_input_log: %v", err)
			os.Exit(1)
		}
		cancel()
	}

	// ── agent_graph（硬依賴）──────────────────────────────────────────────────
	if cfg.Graph.GRPCAddr == "" {
		log.Emergency("graph.grpc_addr is required — graphic_input exists to write the knowledge graph")
		os.Exit(1)
	}
	graphClient, err := services.NewGraphClient(cfg.Graph.GRPCAddr, services.GraphTLS{
		InsecureSkipVerify: cfg.Graph.InsecureSkipVerify,
		ClientCertFile:     cfg.Graph.ClientCertFile,
		ClientKeyFile:      cfg.Graph.ClientKeyFile,
		CAFile:             cfg.Graph.CAFile,
	}, time.Duration(cfg.Graph.DialTimeoutSec)*time.Second, time.Duration(cfg.Graph.CallTimeoutSec)*time.Second)
	if err != nil {
		log.Emergencyf("agent_graph gRPC dial %s failed: %v", cfg.Graph.GRPCAddr, err)
		os.Exit(1)
	}
	defer graphClient.Close()
	log.Infof("GraphService ready: gRPC → %s", cfg.Graph.GRPCAddr)

	// ── Vault / Vision ───────────────────────────────────────────────────────
	vault := services.NewVaultWriter(cfg.Vault.Path, cfg.Vault.InboxDir, cfg.Vault.AttachmentsDir, cfg.Vault.TempDir)
	if vault.Enabled() {
		if err := vault.EnsureDirs(); err != nil {
			log.Warningf("vault dirs: %v — obsidian path may fail", err)
		} else {
			log.Infof("Vault ready: %s (inbox=%s attachments=%s)", cfg.Vault.Path, cfg.Vault.InboxDir, cfg.Vault.AttachmentsDir)
		}
	} else {
		log.Warning("Obsidian path disabled (vault.path is empty) — targets=obsidian uploads will fail")
	}
	vision := services.NewVisionClient(cfg.Vision.OllamaURL, cfg.Vision.Model, cfg.Vision.Prompt, cfg.Vision.TimeoutSec)
	if vision.Enabled() {
		log.Infof("Vision OCR ready: %s (%s)", cfg.Vision.OllamaURL, cfg.Vision.Model)
	} else {
		log.Warning("Vision OCR disabled (vision.ollama_url / model empty) — image uploads will fail")
	}

	inputSvc := &services.InputService{Graph: graphClient, Vault: vault, Vision: vision, DB: db.DB()}
	controllers.SetInputService(inputSvc)

	// ── HTTP server（:8505）─────────────────────────────────────────────────
	srv := server.New(cfg.Hyp(), log)
	setupRoutes(srv)

	// ── gRPC server（:8513；trusted_ips / CN 允許清單同 dev_pub_0.9 機制）────
	mtlsOn := cfg.Server.TLS.Enabled && (cfg.GRPC.CAFile != "" || len(cfg.GRPC.TrustedIPs) > 0)
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
				log.Infof("gRPC access control enabled (CNs: %v, trusted IPs: %v)", cfg.GRPC.AllowedClientCNs, cfg.GRPC.TrustedIPs)
			}
		}
	}
	grpcSrv := grpc.NewServer(grpcOpts...)
	graphic_inputpb.RegisterGraphicInputServiceServer(grpcSrv, controllers.NewGRPCServer(inputSvc))

	lis, lisErr := net.Listen("tcp", cfg.GRPC.Addr)
	if lisErr != nil {
		log.Warningf("gRPC listen %s failed: %v — gRPC server will not start", cfg.GRPC.Addr, lisErr)
	}

	serverErrors := make(chan error, 2)
	go func() {
		log.Infof("HTTP server starting on %s", cfg.Server.Addr)
		serverErrors <- srv.Start()
	}()
	if lis != nil {
		go func() {
			log.Infof("gRPC server starting on %s (GraphicInputService)", cfg.GRPC.Addr)
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
	r.POST("/api/input/upload", controllers.UploadDocs) // multipart，不走 Schema JSON 綁定
	r.GET("/api/input/log", controllers.ListLog)

	api := r.NewGroup("/api")

	api.Schema(schema.Route{
		Method: "POST", Path: "/input/entity",
		Summary: "結構化建立/更新實體（重複偵測：有相似候選時回候選不寫入，force=true 強制）",
		Tags:    []string{"input", "entity"},
		Input:   models.UpsertEntityRequest{},
		Output:  models.UpsertEntityResponse{},
	}).Handle(controllers.UpsertEntity)

	api.Schema(schema.Route{
		Method: "POST", Path: "/input/relation",
		Summary: "結構化建立關係（type 空 → related_to；端點自動補 stub）",
		Tags:    []string{"input", "relation"},
		Input:   models.UpsertRelationRequest{},
	}).Handle(controllers.UpsertRelation)

	api.Schema(schema.Route{
		Method: "POST", Path: "/input/check",
		Summary: "重複偵測預查：回相似實體候選清單",
		Tags:    []string{"input", "check"},
		Input:   models.CheckDuplicatesRequest{},
		Output:  []models.SimilarCandidate{},
	}).Handle(controllers.CheckDuplicates)

	api.Schema(schema.Route{
		Method: "POST", Path: "/input/batch",
		Summary: "批次匯入（rows 或 csv 擇一；entity CSV: name,category,description / relation CSV: source,type,target）",
		Tags:    []string{"input", "batch"},
		Input:   models.BatchImportRequest{},
		Output:  models.BatchImportResponse{},
	}).Handle(controllers.BatchImport)
}
