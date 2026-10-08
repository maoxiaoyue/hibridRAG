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

	appconfig "agent_graph/app/config"
	"agent_graph/app/controllers"
	"agent_graph/app/models"
	"agent_graph/app/services"
	"agent_graph/config"
	"agent_graph/internal/db"
	"agent_graph/internal/logger"
	"agent_graph/internal/mtls"
	"agent_graph/internal/sync"
	"agent_graph/pb/graphpb"
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

	log.Info("Starting agent_graph service...")

	database, err := db.Init(&cfg.Database)
	if err != nil {
		log.Emergencyf("Failed to initialize database: %v", err)
		os.Exit(1)
	}
	defer db.Close()
	bunDB := database.HypDB()

	if cfg.Database.AutoMigrate {
		log.Info("Running database migrations...")
		if err := models.AutoMigrate(bunDB); err != nil {
			log.Errorf("Failed to migrate database: %v", err)
		}
	}

	// ── 圖譜後端：embedded Kùzu（唯一實作；AGE/pgvector 路徑已於 2026-07-08 移除）──
	// PG 仍用於 graph_sync_state / proposed_relations 兩張一般表。
	ctx := context.Background()
	var (
		graphBackend  services.GraphBackend
		vectorBackend services.VectorBackend
	)

	kuzuPath := cfg.Graph.KuzuPath
	if kuzuPath == "" {
		kuzuPath = "data/graph.kz"
	}
	kuzuClient, err := services.NewKuzuClient(kuzuPath)
	if err != nil {
		log.Emergencyf("Failed to open Kùzu database at %s: %v", kuzuPath, err)
		os.Exit(1)
	}
	defer kuzuClient.Close()
	if err := services.EnsureKuzuSchema(ctx, kuzuClient, cfg.Graph.EmbeddingDim); err != nil {
		log.Emergencyf("Failed to ensure Kùzu graph schema: %v", err)
		os.Exit(1)
	}
	log.Infof("Kùzu graph ready: %s", kuzuPath)
	graphBackend = kuzuClient

	if err := services.EnsureKuzuVectorSchema(ctx, kuzuClient, "entity_embedding_idx"); err != nil {
		log.Warningf("Kùzu vector index unavailable (%v) — hybrid vector search disabled", err)
		vectorBackend = services.NewKuzuVectorStore(kuzuClient, false)
	} else {
		log.Info("Kùzu vector index ready: EntityEmbedding")
		vectorBackend = services.NewKuzuVectorStore(kuzuClient, true)
	}

	// ── 固定模型設定：直接打 Ollama，不經過 agent_embedding（app/config/ollama.yaml）──
	var embedClient *services.EmbedClient
	var chatClient *services.ChatClient
	if ollamaCfg, err := appconfig.LoadOllamaConfig("app/config/ollama.yaml"); err != nil {
		log.Warningf("app/config/ollama.yaml load failed (%v) — LLM extraction + embeddings disabled", err)
	} else {
		embedClient = services.NewEmbedClient(ollamaCfg.Embedding)
		chatClient = services.NewChatClient(ollamaCfg.Chat)
		log.Infof("Ollama clients ready: chat=%s embedding=%s", ollamaCfg.Chat.Model, ollamaCfg.Embedding.Model)
	}

	// ── Obsidian vault 設定（app/config/md.yaml）需先於 facade：exporter 要掛進 facade ──
	mdCfg, err := appconfig.LoadMDConfig("app/config/md.yaml")
	if err != nil {
		log.Warningf("app/config/md.yaml load failed (%v) — vault sync disabled", err)
		mdCfg = &appconfig.MDConfig{}
	}
	syncCtx, cancelSync := context.WithCancel(context.Background())

	// 圖譜→vault 鏡像匯出：每個圖譜寫入把受影響實體 render 成 entities/*.md，
	// 啟動時做一次全量 reconcile（含清 stale 檔）。
	var exporter *services.VaultExporter
	if mdCfg.Vault.ExportEnabled() {
		exporter = services.NewVaultExporter(graphBackend, mdCfg.Vault.Path, mdCfg.Vault.ExportDirName(), log)
		go exporter.Run(syncCtx)
		exporter.NotifyAll()
		log.Infof("vault export enabled: %s", exporter.ExportPath())
	}

	facade := &services.GraphFacade{
		Graph: graphBackend, Vectors: vectorBackend, Embed: embedClient, Chat: chatClient,
		DB: bunDB, Exporter: exporter, ConfidenceThreshold: cfg.Graph.ConfidenceThreshold,
	}
	controllers.SetGraphFacade(facade)

	// ── Obsidian vault 同步 worker ──────────────────────────────────────────
	var workerOpts []sync.Option
	if embedClient != nil {
		workerOpts = append(workerOpts, sync.WithEmbed(embedClient))
	}
	if chatClient != nil {
		workerOpts = append(workerOpts, sync.WithChat(chatClient))
	}
	if exporter != nil {
		workerOpts = append(workerOpts, sync.WithExporter(exporter))
	}
	worker := sync.NewWorker(mdCfg.Vault, graphBackend, vectorBackend, bunDB, log, cfg.Graph.ConfidenceThreshold, workerOpts...)
	worker.Start(syncCtx)

	// ── 知識沉澱管線：對話/上傳 → vault/temp → 分類 worker 搬進分類目錄 → 攝取 ──
	// vaultTemp 供 AppendTemp gRPC 寫 temp；DigestWorker 閒置分類 + 每日整理切割。
	var vaultTemp *services.VaultTemp
	if mdCfg.Vault.Path != "" {
		vaultTemp = services.NewVaultTemp(mdCfg.Vault.Path, mdCfg.Vault.TempDirName())
	}
	if mdCfg.Vault.DigestEnabled() && chatClient != nil {
		digest := services.NewDigestWorker(mdCfg.Vault, chatClient, log).
			WithOnFiled(worker.SyncFile) // 分類完的筆記立即攝取進圖譜（不等 fsnotify）
		go digest.Run(syncCtx)
	} else if mdCfg.Vault.DigestEnabled() {
		log.Warningf("knowledge-digest disabled: no Ollama chat client for classification")
	}

	srv := server.New(cfg.Hyp(), log)
	setupRoutes(srv)

	// gRPC server（GraphService）：供 agents 服務的 graph_query 工具呼叫。
	grpcAddr := cfg.GRPC.Addr
	if grpcAddr == "" {
		grpcAddr = ":8512"
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
	graphpb.RegisterGraphServiceServer(grpcSrv,
		controllers.NewGraphGRPCServer(facade, services.NewVaultFS(mdCfg.Vault.Path), vaultTemp))

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
			log.Infof("gRPC server starting on %s (GraphService)", grpcAddr)
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
		cancelSync()
		grpcSrv.GracefulStop()
		os.Exit(1)
	case sig := <-shutdown:
		log.Infof("Shutdown signal received: %v", sig)
		cancelSync()
		grpcSrv.GracefulStop()
		log.Info("Server stopped")
	}
}

func setupRoutes(srv *server.Server) {
	r := srv.Router()

	r.GET("/health", controllers.HealthCheck)

	api := r.NewGroup("/api")

	api.Schema(schema.Route{
		Method: "POST", Path: "/graph/search",
		Summary: "依名稱/描述搜尋實體（substring match）",
		Tags:    []string{"graph", "search"},
		Input:   models.SearchEntitiesRequest{},
		Output:  []models.EntityResult{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			400: {Description: "missing query"},
			503: {Description: "graph disabled"},
		},
	}).Handle(controllers.SearchEntities)

	api.Schema(schema.Route{
		Method: "POST", Path: "/graph/context",
		Summary: "取得某實體的區域圖遍歷結果（N-hop 鄰居 + 關係）",
		Tags:    []string{"graph", "context"},
		Input:   models.LocalContextRequest{},
		Output:  models.LocalContextResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			404: {Description: "entity not found"},
			503: {Description: "graph disabled"},
		},
	}).Handle(controllers.GetLocalContext)

	api.Schema(schema.Route{
		Method: "POST", Path: "/graph/cypher",
		Summary: "執行自訂 openCypher 查詢（execute_cypher escape hatch，內部/除錯用）",
		Tags:    []string{"graph", "cypher"},
		Input:   models.ExecuteCypherRequest{},
		Output:  models.ExecuteCypherResponse{},
	}).Handle(controllers.ExecuteCypher)

	api.Schema(schema.Route{
		Method: "POST", Path: "/graph/hybrid_search",
		Summary: "向量相似度找種子實體 + 圖遍歷擴充上下文",
		Tags:    []string{"graph", "hybrid", "search"},
		Input:   models.HybridSearchRequest{},
		Output:  models.HybridSearchResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			503: {Description: "vector store disabled"},
		},
	}).Handle(controllers.HybridSearch)

	api.Schema(schema.Route{
		Method: "POST", Path: "/graph/propose_relation",
		Summary: "提交低信心關係候選，待人工審核（不直接寫入圖譜）",
		Tags:    []string{"graph", "propose"},
		Input:   models.ProposeRelationRequest{},
		Output:  models.ProposedRelation{},
		Responses: map[int]schema.ResponseSchema{
			201: {Description: "proposed"},
			400: {Description: "invalid request"},
		},
	}).Handle(controllers.ProposeRelation)

	api.Schema(schema.Route{
		Method: "POST", Path: "/graph/proposed/list",
		Summary: "列出待審關係（status 空 = pending，先進先審排序）",
		Tags:    []string{"graph", "propose", "review"},
		Input:   models.ListProposedRequest{},
		Output:  []models.ProposedRelation{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
		},
	}).Handle(controllers.ListProposed)

	api.Schema(schema.Route{
		Method: "POST", Path: "/graph/proposed/approve",
		Summary: "核准待審關係：MERGE 進圖譜並標記 approved",
		Tags:    []string{"graph", "propose", "review"},
		Input:   models.ReviewProposedRequest{},
		Output:  models.ProposedRelation{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "approved"},
			404: {Description: "not found"},
			409: {Description: "already reviewed"},
			503: {Description: "graph disabled"},
		},
	}).Handle(controllers.ApproveProposed)

	api.Schema(schema.Route{
		Method: "POST", Path: "/graph/proposed/reject",
		Summary: "拒絕待審關係：標記 rejected（不碰圖譜）",
		Tags:    []string{"graph", "propose", "review"},
		Input:   models.ReviewProposedRequest{},
		Output:  models.ProposedRelation{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "rejected"},
			404: {Description: "not found"},
			409: {Description: "already reviewed"},
		},
	}).Handle(controllers.RejectProposed)

	// ---- dev_090 graphic_input 配套：直接寫入 + 純文字攝取 ----
	api.Schema(schema.Route{
		Method: "POST", Path: "/graph/entity",
		Summary: "直接建立/更新實體（graphic_input 人工輸入；含 embedding 同步）",
		Tags:    []string{"graph", "write"},
		Input:   models.UpsertEntityRequest{},
		Output:  models.UpsertEntityResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			503: {Description: "graph disabled"},
		},
	}).Handle(controllers.UpsertEntity)

	api.Schema(schema.Route{
		Method: "POST", Path: "/graph/relation",
		Summary: "直接 MERGE 一條關係（自動補 stub 端點）",
		Tags:    []string{"graph", "write"},
		Input:   models.UpsertRelationRequest{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			503: {Description: "graph disabled"},
		},
	}).Handle(controllers.UpsertRelationDirect)

	api.Schema(schema.Route{
		Method: "POST", Path: "/graph/ingest_text",
		Summary: "純文字攝取：LLM 抽取（confidence gate）+ entity embeddings（graphic_input PG 路徑）",
		Tags:    []string{"graph", "write", "ingest"},
		Input:   models.IngestTextRequest{},
		Output:  models.IngestTextResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			503: {Description: "graph or LLM disabled"},
		},
	}).Handle(controllers.IngestText)
}
