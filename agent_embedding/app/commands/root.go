package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maoxiaoyue/hypgo/pkg/schema"
	"github.com/maoxiaoyue/hypgo/pkg/server"
	"github.com/spf13/cobra"

	"agent_embedding/app/controllers"
	"agent_embedding/app/models"
	"agent_embedding/app/services"
	"agent_embedding/config"
	"agent_embedding/internal/cache"
	"agent_embedding/internal/cassandra"
	"agent_embedding/internal/eventbus"
	"agent_embedding/internal/logger"
	"agent_embedding/internal/postgres"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "agent_embedding",
	Short: "BGE-M3 Embedding + Cassandra Vector Search service",
	Long:  "agent_embedding — exposes BGE-M3 dense embeddings and Cassandra-backed ANN vector search.",
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP server",
	RunE:  runServe,
}

func Execute() error { return rootCmd.Execute() }

func init() {
	serveCmd.Flags().StringVar(&cfgFile, "config", "config/config.yaml", "config file path")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log, err := logger.Init(cfg.Logger)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer log.Close()

	log.Info("Starting agent_embedding service...")
	log.Infof("Embedding backend=%s url=%s model=%s dim=%d",
		cfg.Embedding.Backend, cfg.Embedding.BaseURL, cfg.Embedding.Model, cfg.Embedding.Dimension)

	// ── Cassandra ───────────────────────────────────────────────────────────
	sess, err := cassandra.Init(cassandra.Config{
		Hosts:       cfg.Cassandra.Hosts,
		Keyspace:    cfg.Cassandra.Keyspace,
		Username:    cfg.Cassandra.Username,
		Password:    cfg.Cassandra.Password,
		Consistency: cfg.Cassandra.Consistency,
		Timeout:     cfg.Cassandra.Timeout,
		NumConns:    cfg.Cassandra.NumConns,
	})
	if err != nil {
		log.Emergencyf("Cassandra init failed: %v", err)
		os.Exit(1)
	}
	defer cassandra.Close()
	log.Infof("Cassandra connected: %v keyspace=%s", cfg.Cassandra.Hosts, cfg.Cassandra.Keyspace)

	if cfg.Cassandra.AutoInit {
		log.Infof("Ensuring chunks_dense schema (dimension=%d)...", cfg.Embedding.Dimension)
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := cassandra.EnsureSchema(bg, cfg.Embedding.Dimension); err != nil {
			log.Warningf("Schema init failed (continuing): %v", err)
		} else {
			log.Info("chunks_dense schema ready")
		}
		cancel()
	}

	// ── Redis (optional cache) ──────────────────────────────────────────────
	rdb, err := cache.Init(cache.Config{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		Enabled:  cfg.Redis.Enabled,
	})
	if err != nil {
		log.Warningf("Redis init failed (caching disabled): %v", err)
		rdb = nil
	} else if rdb != nil {
		log.Infof("Redis cache ready: %s ttl=%s", cfg.Redis.Addr, cfg.Redis.CacheTTL)
	} else {
		log.Info("Redis cache disabled")
	}
	defer cache.Close()

	// ── Embedding client + Vector DB + Service ──────────────────────────────
	embedder := services.NewEmbeddingClient(
		cfg.Embedding.BaseURL,
		cfg.Embedding.Model,
		services.Backend(cfg.Embedding.Backend),
		cfg.Embedding.Timeout,
		rdb,
		cfg.Redis.CacheTTL,
	)
	vectorDB := services.NewVectorDB(sess)
	embeddingSvc := services.NewEmbeddingService(embedder, vectorDB, cfg.Embedding.MaxBatchSize)

	// ── Event Bus（v2 架構新增；Redis 不可用時自動降級為 nop） ─────────────
	if cfg.EventBus.Enabled && rdb != nil {
		bus := eventbus.NewRedisStream(rdb, eventbus.Config{
			Source:         "agent_embedding",
			MaxLen:         cfg.EventBus.MaxLen,
			PublishTimeout: cfg.EventBus.PublishTimeout,
		})
		embeddingSvc = embeddingSvc.WithEventBus(bus, services.EventStreamNames{
			Upserted: cfg.EventBus.StreamUpserted,
			Searched: cfg.EventBus.StreamSearched,
			Deleted:  cfg.EventBus.StreamDeleted,
		})
		log.Infof("EventBus ready (Redis Stream): upserted=%s searched=%s deleted=%s",
			cfg.EventBus.StreamUpserted, cfg.EventBus.StreamSearched, cfg.EventBus.StreamDeleted)
	} else {
		log.Info("EventBus disabled (eventbus.enabled=false or Redis unavailable)")
	}

	controllers.Init(embeddingSvc)

	// ── PostgreSQL（hybrid search 用，可選） ────────────────────────────────
	pgDB, err := postgres.Init(cfg.Postgres)
	if err != nil {
		log.Warningf("Postgres init failed: %v — hybrid search disabled", err)
	} else if pgDB != nil {
		log.Infof("Postgres connected (max_open=%d)", cfg.Postgres.MaxOpenConns)
	} else {
		log.Info("Postgres disabled (postgres.enabled=false)")
	}
	defer postgres.Close()

	// ── HybridSearcher（dense + sparse + RRF） ──────────────────────────────
	// Sparse 路徑策略（2026-05-08：放棄 BGE-M3 sparse 向量，僅留 FTS / nop）：
	//   "fts"  — PG full-text search（不需 sparse 向量資料；可選）
	//   "nop"  — 不查 sparse；hybrid 結果等同 dense-only
	if cfg.Hybrid.Enabled {
		var sparse services.SparseSearcher
		switch cfg.Hybrid.SparseStrategy {
		case "fts":
			if pgDB == nil {
				log.Warning("hybrid.sparse_strategy=fts but Postgres unavailable; falling back to nop")
				sparse = services.NewNopSparseSearcher()
			} else {
				sparse = services.NewPGTextSearcher(pgDB)
			}
		case "nop", "":
			sparse = services.NewNopSparseSearcher()
		default:
			log.Warningf("unknown hybrid.sparse_strategy=%q; falling back to nop", cfg.Hybrid.SparseStrategy)
			sparse = services.NewNopSparseSearcher()
		}
		hybrid := services.NewHybridSearcher(embeddingSvc, sparse, services.HybridSearchOptions{
			DenseTopN:  cfg.Hybrid.DenseTopN,
			SparseTopN: cfg.Hybrid.SparseTopN,
			FinalTopK:  cfg.Hybrid.FinalTopK,
			RRFK:       cfg.Hybrid.RRFK,
			MinScore:   cfg.Hybrid.MinScore,
		})
		controllers.InitHybrid(hybrid)
		log.Infof("HybridSearcher ready (sparse=%s, k=%d, top_n=%d/%d, final=%d)",
			cfg.Hybrid.SparseStrategy, cfg.Hybrid.RRFK,
			cfg.Hybrid.DenseTopN, cfg.Hybrid.SparseTopN, cfg.Hybrid.FinalTopK)
	} else {
		log.Info("HybridSearcher disabled (hybrid.enabled=false)")
	}

	// Best-effort warm-up: ping the embedding backend.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if dim, err := embedder.Ping(pingCtx); err != nil {
		log.Warningf("Embedding backend probe failed: %v", err)
	} else {
		log.Infof("Embedding backend ready (dim=%d)", dim)
	}
	pingCancel()

	// ── HTTP server ─────────────────────────────────────────────────────────
	srv := server.New(cfg.Hyp(), log)
	setupRoutes(srv)

	serverErrors := make(chan error, 1)
	go func() {
		log.Infof("HTTP server starting on %s", cfg.Server.Addr)
		serverErrors <- srv.Start()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	case sig := <-shutdown:
		log.Infof("Shutdown signal received: %v", sig)
		log.Info("Server stopped")
	}
	return nil
}

func setupRoutes(srv *server.Server) {
	r := srv.Router()
	r.GET("/health", controllers.HealthCheck)

	api := r.NewGroup("/api")

	api.Schema(schema.Route{
		Method: "POST", Path: "/embed",
		Summary: "Embed a single text via BGE-M3 (dense vector)",
		Tags:    []string{"embedding"},
		Input:   models.EmbedRequest{},
		Output:  models.EmbedResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			400: {Description: "invalid request"},
		},
	}).Handle(controllers.Embed)

	api.Schema(schema.Route{
		Method: "POST", Path: "/embed/batch",
		Summary: "Batch-embed multiple texts (dense vectors)",
		Tags:    []string{"embedding"},
		Input:   models.EmbedBatchRequest{},
		Output:  models.EmbedBatchResponse{},
	}).Handle(controllers.EmbedBatch)

	api.Schema(schema.Route{
		Method: "POST", Path: "/upsert",
		Summary: "Embed and store chunks for (agent_id, doc_id)",
		Tags:    []string{"vector-search"},
		Input:   models.UpsertRequest{},
		Output:  models.UpsertResponse{},
	}).Handle(controllers.Upsert)

	api.Schema(schema.Route{
		Method: "POST", Path: "/search/hybrid",
		Summary: "Hybrid search: dense ANN (Cassandra) + sparse FTS (PG) fused via RRF",
		Tags:    []string{"vector-search", "hybrid"},
		Input:   models.HybridSearchRequest{},
		Output:  models.HybridSearchResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			400: {Description: "invalid request"},
			503: {Description: "hybrid disabled (postgres not configured)"},
		},
	}).Handle(controllers.HybridSearch)

	api.Schema(schema.Route{
		Method: "POST", Path: "/search",
		Summary: "ANN vector search by text query or pre-computed vector",
		Tags:    []string{"vector-search"},
		Input:   models.SearchRequest{},
		Output:  models.SearchResponse{},
	}).Handle(controllers.Search)

	api.Schema(schema.Route{
		Method: "DELETE", Path: "/chunks",
		Summary: "Delete all chunks for an (agent_id, doc_id) pair",
		Tags:    []string{"vector-search"},
		Output:  models.DeleteResponse{},
		Params: []schema.ParamSchema{
			{Name: "agent_id", In: "query", Required: true, Type: "string"},
			{Name: "doc_id", In: "query", Required: true, Type: "string"},
		},
	}).Handle(controllers.DeleteByDoc)
}
