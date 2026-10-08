package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/maoxiaoyue/hypgo/pkg/router"
	"github.com/maoxiaoyue/hypgo/pkg/schema"
	"github.com/maoxiaoyue/hypgo/pkg/server"

	"agent/app/controllers"
	"agent/app/models"
	"agent/app/routers"
	"agent/app/services"
	"agent/config"
	ctxbuilder "agent/internal/agent/context"
	"agent/internal/agent/embed"
	"agent/internal/agent/ingest"
	"agent/internal/agent/retrieval"
	"agent/internal/agent/vectordb"
	"agent/internal/cache"
	"agent/internal/database"
	"agent/internal/eventbus"
	"agent/internal/logger"
	"agent/internal/mtls"
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

	log.Info("Starting single_agent service...")

	// ── Model Registry（chat / vision / embedding 模型名單一事實來源） ────────
	// 既有 yaml 仍可寫死 model 名；空字串時依 role 由 registry 解析。
	modelReg := services.DefaultModelRegistry()
	log.Infof("Model registry ready: %d models (chat=%s, tool_calling=%s, vision=%s, embedding=%s)",
		len(modelReg.List()),
		modelReg.MustModelFor(services.RoleChat, "-"),
		modelReg.MustModelFor(services.RoleToolCalling, "-"),
		modelReg.MustModelFor(services.RoleVision, "-"),
		modelReg.MustModelFor(services.RoleEmbedding, "-"),
	)

	db, err := database.Init(cfg.Database)
	if err != nil {
		log.Emergencyf("Failed to initialize database: %v", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := cache.Init(cfg.Redis); err != nil {
		log.Warningf("Failed to initialize Redis: %v", err)
	}
	defer cache.Close()

	if cfg.Database.AutoMigrate {
		log.Info("Running database migrations...")
		if err := models.AutoMigrate(db); err != nil {
			log.Errorf("Failed to migrate database: %v", err)
		}
	}

	// ── Skill Loader（HTTP + gRPC で共有）─────────────────────────────────
	skillsDir := cfg.Skills.Dir
	if skillsDir == "" {
		skillsDir = "./skills"
	}
	skillLoader := services.NewSkillLoader(skillsDir)

	// ── Role Compiler + Compiling Skill Loader ───────────────────────────
	// 每次 SKILL.md 真的被重讀時，自動解析 ## Model 偏好、透過 modelReg 解析出
	// 具體模型，並寫出 skills/{agent_id}/ROLE.md（衍生檔，供除錯/檢視）。
	roleCompiler := services.NewRoleCompiler(modelReg, skillsDir)
	compilingLoader := services.NewCompilingSkillLoader(skillLoader, roleCompiler)

	// ── LLM Client ────────────────────────────────────────────────────────
	var llmClient services.LLMClient
	llmCfg, err := services.LoadLLMConfig("config/llm.yaml")
	if err != nil {
		log.Warningf("LLM config not found (%v) — gRPC agent will run without LLM inference", err)
	} else {
		// mode=ollama 且 model 未指定時，依 role（預設 chat）由 registry 解析。
		// tool_calling 需求的下游若有設 role: tool_calling 即可直接拿到 qwen3.5:9b。
		if llmCfg.Mode == "ollama" && llmCfg.Ollama != nil {
			if err := modelReg.ApplyToOllamaConfig(llmCfg.Ollama, services.RoleChat); err != nil {
				log.Warningf("LLM registry resolve failed (%v) — falling back to explicit yaml model", err)
			} else {
				log.Infof("LLM ollama: url=%s model=%s (role=%q)", llmCfg.Ollama.URL, llmCfg.Ollama.Model, llmCfg.Ollama.Role)
			}
		}
		llmClient, err = services.NewLLMClient(*llmCfg)
		if err != nil {
			log.Warningf("LLM client init failed (%v) — gRPC agent will run without LLM inference", err)
		} else {
			log.Infof("LLM client ready: provider=%s", llmClient.Provider())
		}
	}

	// ── LLM Client Pool（persona 依 ## Model 覆寫；僅 ollama 模式支援） ──────
	// 每個 persona 可各自指定 role/model，實際推理時依 agent_id 動態切換
	// LLMClient，而不是全部共用上面那顆全域 llmClient。anthropic/openai/custom
	// 模式沒有多模型切換需求（也還沒有對應的 client 建構樣板），維持 nil —
	// resolveLLM 會在 llmPool 為 nil 時一律 fallback 回全域 llmClient。
	var llmPool *services.LLMClientPool
	if llmCfg != nil && llmCfg.Mode == "ollama" && llmCfg.Ollama != nil {
		llmPool = services.NewLLMClientPool(*llmCfg.Ollama)
		log.Infof("LLM client pool ready (ollama-only per-persona model dispatch): base_url=%s", llmCfg.Ollama.URL)
	} else {
		log.Info("LLM client pool disabled (mode != ollama) — persona ## Model overrides will be ignored")
	}

	// ── Outbound Dispatcher ───────────────────────────────────────────────
	var dispatcher services.Dispatcher
	downCfg, err := services.LoadDownstreamConfig("config/config.yaml")
	if err != nil {
		log.Warningf("Downstream config error (%v) — outbound dispatch disabled", err)
	} else {
		d := services.NewGrpcDispatcher(downCfg)
		dispatcher = d
		defer d.Close()
		log.Infof("Outbound dispatcher ready: %d downstream agents", len(downCfg.Agents))
	}

	// ── MemoryService ────────────────────────────────────────────────────
	// 所有記憶資料一律透過 gRPC 打 agent_memory；本服務不再直連 Postgres 存取
	// 記憶（見 agent_memory/CLAUDE.md、root CLAUDE.md 的遷移紀錄）。
	if cfg.Memory.GRPCAddr == "" {
		log.Emergency("memory.grpc_addr is required — agent_memory gRPC endpoint must be configured")
		os.Exit(1)
	}
	memSvc, err := services.NewMemoryGRPCClient(
		cfg.Memory.GRPCAddr,
		services.GRPCClientTLS{
			InsecureSkipVerify: cfg.Memory.InsecureSkipVerify,
			ClientCertFile:     cfg.Memory.ClientCertFile,
			ClientKeyFile:      cfg.Memory.ClientKeyFile,
			CAFile:             cfg.Memory.CAFile,
		},
		time.Duration(cfg.Memory.DialTimeoutSec)*time.Second,
		time.Duration(cfg.Memory.CallTimeoutSec)*time.Second,
	)
	if err != nil {
		log.Emergencyf("agent_memory gRPC dial %s failed: %v", cfg.Memory.GRPCAddr, err)
		os.Exit(1)
	}
	defer memSvc.Close()
	log.Infof("MemoryService ready: gRPC → %s", cfg.Memory.GRPCAddr)

	// ── Critic（A3 Critic/Gatekeeper：agent_critic gRPC，選配）─────────────
	// grpc_addr 留空或撥號失敗都只停用評分，不阻擋啟動——跟 memory 不同，
	// Critic 是品質加分項而非推理鏈的硬依賴。
	var criticClient *services.CriticGRPCClient
	if cfg.Critic.GRPCAddr != "" {
		criticClient, err = services.NewCriticGRPCClient(
			cfg.Critic.GRPCAddr,
			services.GRPCClientTLS{
				InsecureSkipVerify: cfg.Critic.InsecureSkipVerify,
				ClientCertFile:     cfg.Critic.ClientCertFile,
				ClientKeyFile:      cfg.Critic.ClientKeyFile,
				CAFile:             cfg.Critic.CAFile,
			},
			time.Duration(cfg.Critic.DialTimeoutSec)*time.Second,
			time.Duration(cfg.Critic.CallTimeoutSec)*time.Second,
		)
		if err != nil {
			log.Warningf("agent_critic gRPC dial %s failed: %v — answer quality evaluation disabled", cfg.Critic.GRPCAddr, err)
			criticClient = nil
		} else {
			defer criticClient.Close()
			log.Infof("CriticService ready: gRPC → %s (max_revisions=%d)", cfg.Critic.GRPCAddr, cfg.Critic.MaxRevisions)
		}
	} else {
		log.Info("Critic evaluation disabled (critic.grpc_addr is empty)")
	}

	// ── Graph（A4 Knowledge Agent：agent_graph gRPC，選配）───────────────────
	// 跟 Critic 一樣是加分項而非硬依賴：grpc_addr 留空或撥號失敗只停用
	// graph_query 工具，不阻擋啟動。
	var graphClient *services.GraphGRPCClient
	if cfg.Graph.GRPCAddr != "" {
		graphClient, err = services.NewGraphGRPCClient(
			cfg.Graph.GRPCAddr,
			services.GRPCClientTLS{
				InsecureSkipVerify: cfg.Graph.InsecureSkipVerify,
				ClientCertFile:     cfg.Graph.ClientCertFile,
				ClientKeyFile:      cfg.Graph.ClientKeyFile,
				CAFile:             cfg.Graph.CAFile,
			},
			time.Duration(cfg.Graph.DialTimeoutSec)*time.Second,
			time.Duration(cfg.Graph.CallTimeoutSec)*time.Second,
		)
		if err != nil {
			log.Warningf("agent_graph gRPC dial %s failed: %v — graph_query tool disabled", cfg.Graph.GRPCAddr, err)
			graphClient = nil
		} else {
			defer graphClient.Close()
			log.Infof("GraphService ready: gRPC → %s", cfg.Graph.GRPCAddr)
		}
	} else {
		log.Info("Graph query tool disabled (graph.grpc_addr is empty)")
	}

	// ── Semantic memory（LongTermMemory：embed.Client + agent_memory gRPC）───
	var semanticMem *services.SemanticMemory
	if cfg.Embedding.Provider != "" {
		embedClient := embed.New(cfg.Embedding.TEIEndpoint,
			embed.WithModel(cfg.Embedding.Model),
			embed.WithBackend(embed.Backend(cfg.Embedding.Provider)),
			embed.WithTimeout(30*time.Second),
		)
		semanticMem = services.NewSemanticMemory(embedClient, memSvc)
		log.Infof("SemanticMemory ready: embed=%s(%s) → agent_memory gRPC", cfg.Embedding.Provider, cfg.Embedding.Model)
	} else {
		log.Info("SemanticMemory disabled (embedding.provider not set)")
	}

	// ── Shared memory（跨 agent KV pool）───────────────────────────────────
	var sharedReader services.SharedReaderForTool = memSvc
	var sharedWriter services.SharedWriterForTool = memSvc
	if cfg.Agent.PoolID == "" {
		log.Info("SharedMemory: agent.pool_id not set — shared_read/write will require an explicit pool_id per call")
	} else {
		log.Infof("SharedMemory ready: pool_id=%s", cfg.Agent.PoolID)
	}

	// ── IngestRunner ─────────────────────────────────────────────────────
	{
		runnerOpts := []ingest.RunnerOption{}
		if cfg.Embedding.Mode == "http" && cfg.Embedding.HTTPBaseURL != "" {
			timeout := time.Duration(cfg.Embedding.TimeoutSec) * time.Second
			if timeout <= 0 {
				timeout = 30 * time.Second
			}
			kvStore := vectordb.NewHTTPClient(cfg.Embedding.HTTPBaseURL, timeout)
			runnerOpts = append(runnerOpts, ingest.WithKnowledgeStore(kvStore))
			log.Infof("IngestRunner: HTTP mode → %s (timeout=%s)", cfg.Embedding.HTTPBaseURL, timeout)
		} else {
			log.Info("IngestRunner: RunReferences disabled (embedding.mode != http)")
		}
		runner := ingest.NewRunner(memSvc, runnerOpts...)
		controllers.SetIngestRunner(runner)
	}

	// ── 上傳端點設定（落地目錄 + 視覺抽文字） ──────────────────────────────
	{
		var vision *services.VisionExtractor
		if cfg.Ingest.Vision.Enabled {
			vurl := cfg.Ingest.Vision.OllamaURL
			vmodel := cfg.Ingest.Vision.Model
			// 未明示 model → 由 registry 依 role 解析（預設 RoleVision）。
			if vmodel == "" {
				role := services.ModelRole(cfg.Ingest.Vision.Role)
				if role == "" {
					role = services.RoleVision
				}
				if m, err := modelReg.Resolve(role); err == nil {
					vmodel = m.Name
					if vurl == "" {
						vurl = m.ResolvedHost()
					}
				} else {
					log.Warningf("Vision registry resolve(role=%s) failed: %v", role, err)
				}
			}
			if vurl != "" && vmodel != "" {
				vision = services.NewVisionExtractor(vurl, vmodel, cfg.Ingest.Vision.Prompt, cfg.Ingest.Vision.TimeoutSec)
				log.Infof("Vision extractor ready: %s model=%s (role=%q)", vurl, vmodel, cfg.Ingest.Vision.Role)
			} else {
				log.Info("Vision extractor disabled (missing url/model after registry resolve) — image upload will be rejected")
			}
		} else {
			log.Info("Vision extractor disabled (ingest.vision.enabled=false) — image upload will be rejected")
		}
		controllers.SetUploadConfig(cfg.Ingest.UploadsDir, vision)
	}

	// ── Event Bus（v2 架構：Redis Stream） ────────────────────────────────
	if cfg.EventBus.Enabled {
		rdb := cache.GetClient()
		if rdb == nil {
			log.Warning("EventBus enabled but Redis client unavailable — events disabled")
		} else {
			pubTimeout := time.Duration(cfg.EventBus.PublishTimeoutSec) * time.Second
			if pubTimeout <= 0 {
				pubTimeout = 1 * time.Second
			}
			bus := eventbus.NewRedisStream(rdb, eventbus.Config{
				Source:         cfg.Agent.ID,
				MaxLen:         cfg.EventBus.MaxLen,
				PublishTimeout: pubTimeout,
			})
			streams := services.IngestStreams{
				Started:   defaultStream(cfg.EventBus.StreamIngestStart, "ingest.started"),
				Completed: defaultStream(cfg.EventBus.StreamIngestDone, "ingest.completed"),
				Failed:    defaultStream(cfg.EventBus.StreamIngestFailed, "ingest.failed"),
			}
			controllers.SetIngestEventBus(bus, streams)
			log.Infof("EventBus ready (Redis Stream): %s / %s / %s",
				streams.Started, streams.Completed, streams.Failed)
		}
	} else {
		log.Info("EventBus disabled (eventbus.enabled=false)")
	}

	// ── ContextBuilder（Agent 內部模組：RAG + Memory 組裝） ───────────────
	// RAG 路徑：vectordb.HTTPClient（dense） → 包進 retrieval.HybridSearcher
	// （sparse 預設 NopSparseSearcher，即 dense-only；之後可注入 PG FTS / remote sparse）
	var ragSearcher ctxbuilder.RAGSearcher
	if cfg.Embedding.Mode == "http" && cfg.Embedding.HTTPBaseURL != "" {
		timeout := time.Duration(cfg.Embedding.TimeoutSec) * time.Second
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		denseClient := vectordb.NewHTTPClient(cfg.Embedding.HTTPBaseURL, timeout)
		ragSearcher = retrieval.New(denseClient, nil, retrieval.Options{
			DenseTopN:           cfg.Retrieval.DenseTopN,
			SparseTopN:          cfg.Retrieval.SparseTopN,
			FinalTopK:           cfg.Retrieval.FinalTopK,
			RRFK:                cfg.Retrieval.RRFK,
			SimilarityThreshold: cfg.Retrieval.SimilarityThreshold,
		})
		log.Infof("HybridSearcher ready (dense=HTTP %s, sparse=nop; top_n=%d/%d, k=%d, threshold=%.2f)",
			cfg.Embedding.HTTPBaseURL,
			cfg.Retrieval.DenseTopN, cfg.Retrieval.SparseTopN,
			cfg.Retrieval.RRFK, cfg.Retrieval.SimilarityThreshold)
	}
	contextBuilder := ctxbuilder.New(memSvc, ragSearcher, ctxbuilder.Quotas{
		Personal:   negIfZero(cfg.Context.Personal),
		Procedural: negIfZero(cfg.Context.Procedural),
		RAG:        negIfZero(cfg.Context.RAG),
		Episodic:   negIfZero(cfg.Context.Episodic),
		Semantic:   negIfZero(cfg.Context.Semantic),
		MaxChars:   cfg.Context.MaxChars,
		MaxTokens:  cfg.Context.MaxTokens,
	})
	if semanticMem != nil {
		contextBuilder = contextBuilder.WithSemanticSearcher(semanticMem)
	}
	if ragSearcher != nil {
		log.Infof("ContextBuilder ready (RAG via %s; quotas: personal=%d procedural=%d rag=%d episodic=%d; budget: chars=%d tokens=%d)",
			cfg.Embedding.HTTPBaseURL,
			cfg.Context.Personal, cfg.Context.Procedural, cfg.Context.RAG, cfg.Context.Episodic,
			cfg.Context.MaxChars, cfg.Context.MaxTokens)
	} else {
		log.Info("ContextBuilder ready (no RAG; memory-only mode)")
	}

	// ── SessionStore（短期記憶：/api/chat 多輪對話歷史，Redis-backed） ────
	sessionStore := services.NewSessionStore(cache.GetClient(),
		cfg.ChatSession.MaxTurns, time.Duration(cfg.ChatSession.TTLSec)*time.Second)
	if sessionStore != nil {
		log.Infof("SessionStore ready: Redis-backed, max_turns=%d ttl=%s", cfg.ChatSession.MaxTurns,
			time.Duration(cfg.ChatSession.TTLSec)*time.Second)
	} else {
		log.Info("SessionStore disabled (Redis unavailable) — /api/chat stays single-turn-per-request")
	}

	// ── HTTP / HypGo Server ───────────────────────────────────────────────
	// persona 模型設定端點（內網專用）：用 compilingLoader force reload 以刷新
	// ROLE.md + ModelResolution，讓 client 改的 model 立即在 /api/chat 生效。
	controllers.SetPersonaAdmin(services.NewPersonaAdmin(skillsDir, compilingLoader))
	// chat 階段追蹤（client 每秒輪詢「目前哪個 Agent 在處理」）：與 AgentServer 共用同一顆 tracker。
	chatStatusTracker := services.NewChatStatusTracker()
	controllers.SetChatStatusTracker(chatStatusTracker)
	srv := server.New(cfg.Hyp(), log)
	setupRoutes(srv, cfg, skillLoader)

	// ── gRPC Server ───────────────────────────────────────────────────────
	agentID := cfg.Agent.ID
	if agentID == "" {
		agentID = "agent-main"
	}
	grpcAddr := cfg.GRPC.Addr
	if grpcAddr == "" {
		grpcAddr = ":9091"
	}

	// Worker pool（限流；推理任務有界 backpressure）
	workerPool := services.NewWorkerPool(cfg.GRPC.Workers, cfg.GRPC.QueueSize)
	log.Infof("Worker pool ready: workers=%d queue=%d", workerPool.Workers(), cfg.GRPC.QueueSize)

	// ── Tool Registry（內建：memory_read / memory_write / rag_query / graph_query） ─────
	toolReg := services.NewToolRegistry()
	toolReg.MustRegister(&services.MemoryReadTool{
		AgentID: agentID, PoolID: cfg.Agent.PoolID,
		Reader: memSvc, Semantic: semanticMem, Shared: sharedReader,
	})
	toolReg.MustRegister(&services.MemoryWriteTool{
		AgentID: agentID, PoolID: cfg.Agent.PoolID,
		Writer: memSvc, Semantic: semanticMem, Shared: sharedWriter,
	})
	if ragSearcher != nil {
		toolReg.MustRegister(&services.RAGQueryTool{
			AgentID:  agentID,
			Searcher: &ragToolAdapter{inner: ragSearcher},
		})
	}
	// graphClient 為具體型別的 *nil* 包進 GraphQuerierForTool interface 不等於
	// 「未設定」（interface 本身 non-nil）——只在真的撥號成功時才註冊這個工具，
	// 比照 WithCritic 的同一個 nil-interface 陷阱處理方式。
	if graphClient != nil {
		toolReg.MustRegister(&services.GraphQueryTool{Querier: graphClient})
	}
	// web_browse：透過遠端 browserless 上網查資料，工具端先摘要再回。
	// browser.ws_url 留空 → 不註冊（部署環境有 browserless sidecar 才啟用）。
	if cfg.Browser.WSURL != "" {
		toolReg.MustRegister(&services.WebBrowseTool{
			Fetcher:      services.NewBrowserFetcher(cfg.Browser.WSURL, cfg.Browser.NavTimeoutSec),
			Summarizer:   llmClient, // 工具端摘要用主對話 LLM
			MaxBodyChars: cfg.Browser.MaxBodyChars,
		})
		log.Infof("web_browse tool enabled (browserless=%s)", cfg.Browser.WSURL)
	}
	if _, ok := llmClient.(services.ToolCallerLLM); ok {
		log.Infof("Tool registry ready: %d tools; LLM supports tool calling", len(toolReg.List()))
	} else {
		log.Infof("Tool registry ready: %d tools; LLM does NOT support tool calling — will fall back to plain Chat", len(toolReg.List()))
	}

	agentSrv := controllers.NewAgentServer(agentID, compilingLoader, llmClient, dispatcher).
		WithContextBuilder(contextBuilder).
		WithEpisodicWriter(memSvc).
		WithSessionStore(sessionStore).
		WithWorkerPool(workerPool).
		WithToolRegistry(toolReg, services.ToolLoopOptions{
			MaxIterations:  cfg.Tools.MaxIterations,
			ToolTimeoutSec: cfg.Tools.ToolTimeoutSec,
		}).
		WithModelResolver(compilingLoader).
		WithLLMPool(llmPool).
		WithStatus(chatStatusTracker)
	// 對話結束後把問答沉澱進 agent_graph 的 vault/temp（知識沉澱管線）；
	// 重用 graph_query 工具的同一條 gRPC 連線，未接線（graphClient==nil）時跳過。
	if graphClient != nil {
		agentSrv = agentSrv.WithTempAppender(graphClient)
	}

	// ── Ollama/GPU 使用狀況監控 ────────────────────────────────────────────
	// 觀測目標：config 的 monitoring.gpu.ollama_urls；未設定則用 llm.yaml 的
	// chat Ollama URL。推理前 CheckBeforeInference 確認狀態；背景 goroutine
	// 空閒時每 idle_interval（預設 10 分鐘）觀測一次；GET /api/monitor/gpu 可查。
	monCtx, cancelMon := context.WithCancel(context.Background())
	defer cancelMon()
	gpuCfg := cfg.Monitoring.GPU
	gpuEnabled := gpuCfg.Enabled == nil || *gpuCfg.Enabled
	gpuURLs := gpuCfg.OllamaURLs
	if len(gpuURLs) == 0 && llmCfg != nil && llmCfg.Mode == "ollama" && llmCfg.Ollama != nil && llmCfg.Ollama.URL != "" {
		gpuURLs = []string{llmCfg.Ollama.URL}
	}
	if gpuEnabled && len(gpuURLs) > 0 {
		gpuMon := services.NewGPUMonitor(gpuURLs, services.GPUMonitorOptions{
			StaleAfter:      time.Duration(gpuCfg.StaleAfterSec) * time.Second,
			IdleInterval:    time.Duration(gpuCfg.IdleIntervalSec) * time.Second,
			UseNvidiaSMI:    gpuCfg.NvidiaSMI == nil || *gpuCfg.NvidiaSMI,
			UtilWarnPercent: gpuCfg.UtilWarnPercent,
			VRAMWarnPercent: gpuCfg.VRAMWarnPercent,
		})
		go gpuMon.Run(monCtx)
		controllers.SetGPUMonitor(gpuMon)
		agentSrv = agentSrv.WithGPUMonitor(gpuMon)
		log.Infof("GPU monitor ready: hosts=%v", gpuURLs)
	} else if gpuEnabled {
		log.Info("GPU monitor disabled: no Ollama URL to observe (monitoring.gpu.ollama_urls empty, llm mode not ollama)")
	}
	// criticClient 為具體型別的 *nil* 包進 CriticEvaluator interface 不等於「未設定」
	// （interface 本身 non-nil），只在真的撥號成功時才呼叫 WithCritic，維持
	// s.critic 是 true nil interface（reviseWithCritic 的 nil 判斷才會正確生效）。
	if criticClient != nil {
		agentSrv = agentSrv.WithCritic(criticClient, cfg.Critic.MaxRevisions)
	}
	controllers.InitA2A(agentSrv, cfg)
	controllers.SetChatServer(agentSrv) // 同步對話端點 POST /api/chat 與 gRPC 共用同一 AgentServer

	// ── Registry 自動註冊（registry.url 留空 → 停用）────────────────────────
	// 卡片 builder 與 /.well-known/agent-card.json 共用（InitA2A 建的那顆），
	// skills 用 compilingLoader：心跳重建卡片時順帶感知 SKILL.md 變更。
	var registrar *services.RegistryRegistrar
	if cfg.Registry.URL != "" {
		grpcEP := cfg.Registry.GRPCEndpoint
		if grpcEP == "" && strings.HasPrefix(grpcAddr, ":") {
			grpcEP = "localhost" + grpcAddr
		} else if grpcEP == "" {
			grpcEP = grpcAddr
		}
		registrar = services.NewRegistryRegistrar(services.RegistrarOptions{
			RegistryURL:        cfg.Registry.URL,
			AgentID:            agentID,
			PublicURL:          cfg.Registry.PublicURL,
			GRPCEndpoint:       grpcEP,
			HeartbeatSec:       cfg.Registry.HeartbeatSec,
			InsecureSkipVerify: cfg.Registry.InsecureSkipVerify,
		}, controllers.A2ACardBuilder(), compilingLoader)
		registrar.Start()
	} else {
		log.Info("Registry auto-registration disabled (registry.url is empty)")
	}

	// gRPC server：注入 trace_id metadata interceptor + TLS（沿用 HTTP server 的憑證）。
	// dev_pub_0.9：grpc.ca_file 非空即啟用 mTLS（RequireAndVerifyClientCert）+
	// CN 允許清單（外網部署時只放行 agent_gateway）。
	mtlsOn := cfg.Server.TLS.Enabled && cfg.GRPC.CAFile != ""
	grpcOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			mtls.UnaryAllowlist(mtlsOn, cfg.GRPC.AllowedClientCNs, cfg.GRPC.TrustedIPs),
			services.UnaryTraceServerInterceptor(),
		),
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
	models.RegisterAgentServiceServer(grpcSrv, agentSrv)

	lis, lisErr := net.Listen("tcp", grpcAddr)
	if lisErr != nil {
		log.Warningf("gRPC listen %s failed: %v — gRPC server will not start", grpcAddr, lisErr)
	}

	// ── Start both servers ────────────────────────────────────────────────
	serverErrors := make(chan error, 2)

	go func() {
		log.Infof("HTTP server starting on %s (protocol=%s)", cfg.Server.Addr, cfg.Server.Protocol)
		serverErrors <- srv.Start()
	}()

	if lis != nil {
		go func() {
			log.Infof("gRPC server starting on %s (agent_id=%s)", grpcAddr, agentID)
			if err := grpcSrv.Serve(lis); err != nil {
				serverErrors <- fmt.Errorf("gRPC: %w", err)
			}
		}()
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		log.Emergencyf("Server error: %v", err)
		grpcSrv.GracefulStop()
		os.Exit(1)
	case sig := <-shutdown:
		log.Infof("Shutdown signal received: %v", sig)
		if registrar != nil {
			registrar.Stop() // 停心跳 + best-effort 反註冊，讓 registry 不留殭屍記錄
			log.Info("Registry deregistered")
		}
		grpcSrv.GracefulStop()
		log.Info("gRPC server stopped")
		workerPool.Stop()
		log.Info("Worker pool drained")
		log.Info("Server stopped")
	}
}

// defaultStream returns s if non-empty, otherwise the fallback name.
func defaultStream(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// negIfZero 把 0 轉成 -1，讓 ContextBuilder.Quotas.Resolve 套用 builtin 預設值；
// 非 0 直接傳遞（包括負值——明確要求 0 配額者不在這裡處理）
// ragToolAdapter 把 ctxbuilder.RAGSearcher（回 []vectordb.SearchHit）
// 適配成 services.RAGSearcherForTool（回 []services.RAGHit）。
type ragToolAdapter struct {
	inner ctxbuilder.RAGSearcher
}

func (a *ragToolAdapter) SearchByQuery(ctx context.Context, agentID, query string, topK int) ([]services.RAGHit, error) {
	hits, err := a.inner.SearchByQuery(ctx, agentID, query, topK)
	if err != nil {
		return nil, err
	}
	out := make([]services.RAGHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, services.RAGHit{
			ChunkID:    h.ChunkID,
			DocID:      h.DocID,
			Text:       h.Text,
			Similarity: h.Similarity,
		})
	}
	return out, nil
}

func negIfZero(v int) int {
	if v == 0 {
		return -1
	}
	return v
}

// setupRoutes registers all HTTP routes.
// skillLoader is shared with the gRPC AgentServer (same in-memory cache).
func setupRoutes(srv *server.Server, cfg *config.Config, skillLoader *services.SkillLoader) {
	r := srv.Router()

	r.GET("/.well-known/agent-card.json", controllers.A2ACardHandler)
	r.POST("/", controllers.A2AHandler)

	r.GET("/health", controllers.HealthCheck)
	r.GET("/metrics", controllers.Metrics)
	r.GET("/api/monitor/gpu", controllers.GPUStatus)

	registerAgentRoutes(r)

	skillSelector := services.NewSkillSelector(skillLoader)
	controllers.SetSkillRuntime(skillLoader, skillSelector)
	registerSkillRoutes(r)
	registerPersonaRoutes(r)

	// Schema registration for hyp context / contract testing / manifest
	routers.RegisterAgent()
	routers.RegisterSkill()
	routers.RegisterIngest()
}

// registerAgentRoutes registers the RAG agent REST endpoints.
func registerAgentRoutes(r *router.Router) {
	agent := r.NewGroup("/api")

	// ---- Ingest ----
	agent.Schema(schema.Route{
		Method: "POST", Path: "/ingest/skill",
		Summary: "觸發 SKILL.md 解析 + chunk + embed", Tags: []string{"ingest", "skill"},
		Input:  models.IngestSkillRequest{},
		Output: models.IngestJobResponse{},
		Responses: map[int]schema.ResponseSchema{
			202: {Description: "job accepted (async)"},
		},
	}).Handle(controllers.IngestSkill)

	agent.Schema(schema.Route{
		Method: "POST", Path: "/ingest/references",
		Summary: "批次處理 references/*.md", Tags: []string{"ingest", "references"},
		Input:  models.IngestReferencesRequest{},
		Output: models.IngestJobResponse{},
		Responses: map[int]schema.ResponseSchema{
			202: {Description: "job accepted (async)"},
		},
	}).Handle(controllers.IngestReferences)

	agent.Schema(schema.Route{
		Method: "GET", Path: "/ingest/jobs/:id",
		Summary: "查詢攝取任務狀態", Tags: []string{"ingest"},
		Output: models.IngestJob{},
		Params: []schema.ParamSchema{{Name: "id", In: "path", Required: true, Type: "string"}},
	}).Handle(controllers.GetIngestJob)

	agent.Schema(schema.Route{
		Method: "POST", Path: "/ingest/upload",
		Summary: "上傳檔案（PDF/圖片/文字，multipart）→ 抽文字 → 攝取", Tags: []string{"ingest", "upload"},
		Responses: map[int]schema.ResponseSchema{
			202: {Description: "job accepted (async)"},
			400: {Description: "invalid form / no files"},
			422: {Description: "all files failed extraction"},
		},
	}).Handle(controllers.IngestUpload)

	// ---- Chat（同步對話 + client 端工具回合制） ----
	agent.Schema(schema.Route{
		Method: "POST", Path: "/chat",
		Summary: "同步對話；支援 client 端工具回合制（status=tool_calls / final）", Tags: []string{"chat"},
		Input:  models.ChatTurnRequest{},
		Output: models.ChatTurnResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "final 或 tool_calls"},
			400: {Description: "invalid request"},
			503: {Description: "no LLM configured"},
		},
	}).Handle(controllers.Chat)

	// 對話階段狀態（client 每秒輪詢；經 gateway 內部 HTTP 呼叫，client 不直接打）。
	agent.Schema(schema.Route{
		Method: "GET", Path: "/chat/status/:session_id",
		Summary: "回報 session 目前處理到哪個 Agent 階段", Tags: []string{"chat", "status"},
		Params: []schema.ParamSchema{{Name: "session_id", In: "path", Required: true, Type: "string"}},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok（stage + active）"},
		},
	}).Handle(controllers.ChatStatus)

	// 對話附加圖片辨識（VisionExtractor 抽文字 + 同時 ingest；經 gateway ChatImage 轉發）。
	agent.Schema(schema.Route{
		Method: "POST", Path: "/chat/image",
		Summary: "附加圖片抽文字（供當下對話辨識）+ ingest 進 RAG", Tags: []string{"chat", "vision"},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok（抽出的文字）"},
			400: {Description: "invalid request"},
			422: {Description: "vision extraction failed / disabled"},
		},
	}).Handle(controllers.ChatImage)
}

// registerSkillRoutes registers the skill runtime REST endpoints.
func registerSkillRoutes(r *router.Router) {
	g := r.NewGroup("/api")

	g.Schema(schema.Route{
		Method: "POST", Path: "/skill/load",
		Summary: "載入 skills/{agent_id}/SKILL.md 與 references/", Tags: []string{"skill", "runtime"},
		Input:  models.LoadSkillRequest{},
		Output: models.LoadSkillResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "loaded or cache hit"},
			400: {Description: "missing agent_id"},
			404: {Description: "skills/{agent_id} not found"},
		},
	}).Handle(controllers.LoadSkill)

	g.Schema(schema.Route{
		Method: "GET", Path: "/skill/:agent_id",
		Summary: "取得 cache 中的 SkillPack（不觸發重讀）", Tags: []string{"skill", "runtime"},
		Output: models.SkillPack{},
		Params: []schema.ParamSchema{{Name: "agent_id", In: "path", Required: true, Type: "string"}},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"}, 404: {Description: "not loaded"},
		},
	}).Handle(controllers.GetSkill)

	g.Schema(schema.Route{
		Method: "POST", Path: "/skill/select",
		Summary: "依 query 從 references/ 動態挑選片段", Tags: []string{"skill", "runtime", "retrieval"},
		Input:  models.SelectReferencesRequest{},
		Output: models.SelectReferencesResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			400: {Description: "missing agent_id or query"},
			404: {Description: "agent not loaded"},
		},
	}).Handle(controllers.SelectSkillReferences)
}

// registerPersonaRoutes registers persona 模型設定端點（內網 / dev_090 專用）。
func registerPersonaRoutes(r *router.Router) {
	g := r.NewGroup("/api")

	g.Schema(schema.Route{
		Method: "GET", Path: "/personas",
		Summary: "列出所有 persona 與其目前推理模型", Tags: []string{"persona", "model"},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			503: {Description: "persona admin not configured"},
		},
	}).Handle(controllers.ListPersonas)

	g.Schema(schema.Route{
		Method: "GET", Path: "/personas/:agent_id",
		Summary: "取得單一 persona 的模型設定現況", Tags: []string{"persona", "model"},
		Params: []schema.ParamSchema{{Name: "agent_id", In: "path", Required: true, Type: "string"}},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"}, 404: {Description: "not found"},
		},
	}).Handle(controllers.GetPersona)

	g.Schema(schema.Route{
		Method: "PUT", Path: "/personas/:agent_id/model",
		Summary: "設定 persona 推理模型（寫 SKILL.md ## Model 段 + reload，立即生效）",
		Tags:    []string{"persona", "model"},
		Input:   controllers.SetPersonaModelRequest{},
		Params:  []schema.ParamSchema{{Name: "agent_id", In: "path", Required: true, Type: "string"}},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "updated"},
			400: {Description: "invalid request"},
			503: {Description: "persona admin not configured"},
		},
	}).Handle(controllers.SetPersonaModel)

	g.Schema(schema.Route{
		Method: "GET", Path: "/personas/:agent_id/skill",
		Summary: "取得 persona SKILL.md 完整原始文字（供編輯器載入）",
		Tags:    []string{"persona", "skill"},
		Params:  []schema.ParamSchema{{Name: "agent_id", In: "path", Required: true, Type: "string"}},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"}, 404: {Description: "not found"},
		},
	}).Handle(controllers.GetPersonaSkill)

	g.Schema(schema.Route{
		Method: "PUT", Path: "/personas/:agent_id/skill",
		Summary: "整份覆寫 persona 的 SKILL.md + reload，立即生效",
		Tags:    []string{"persona", "skill"},
		Input:   controllers.SetPersonaSkillRequest{},
		Params:  []schema.ParamSchema{{Name: "agent_id", In: "path", Required: true, Type: "string"}},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "updated"},
			400: {Description: "invalid request"},
			503: {Description: "persona admin not configured"},
		},
	}).Handle(controllers.SetPersonaSkill)
}
