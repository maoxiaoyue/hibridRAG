package commands

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/maoxiaoyue/hypgo/pkg/server"
	"github.com/maoxiaoyue/hypgo/pkg/schema"
	"github.com/spf13/cobra"

	"agent_registry/app/controllers"
	"agent_registry/app/models"
	"agent_registry/config"
	"agent_registry/internal/db"
	"agent_registry/internal/logger"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "agent_registry",
	Short: "A2A Agent Registry",
	Long:  "Agent Registry — dynamic discovery and registration for A2A agents.",
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP server",
	RunE:  runServe,
}

func Execute() error {
	return rootCmd.Execute()
}

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

	log.Info("Starting agent_registry service...")

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
		if err := models.MigrateModelCatalog(database.HypDB()); err != nil {
			log.Errorf("Failed to migrate model catalog: %v", err)
		}
	}

	controllers.SetModelCatalogHost(cfg.Ollama.GetHost())

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

	reg := r.NewGroup("/registry")

	reg.Schema(schema.Route{
		Method:  "POST",
		Path:    "/register",
		Summary: "Register or refresh an A2A agent",
		Tags:    []string{"registry"},
		Input:   models.RegisterAgentRequest{},
		Output:  models.AgentRegistration{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "registered"},
			400: {Description: "invalid request"},
		},
	}).Handle(controllers.Register)

	reg.Schema(schema.Route{
		Method:  "GET",
		Path:    "/agents/:id",
		Summary: "Get an agent by ID",
		Tags:    []string{"registry"},
		Output:  models.AgentRegistration{},
		Params:  []schema.ParamSchema{{Name: "id", In: "path", Required: true, Type: "string"}},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			404: {Description: "not found"},
		},
	}).Handle(controllers.GetAgent)

	reg.Schema(schema.Route{
		Method:  "GET",
		Path:    "/search",
		Summary: "Search agents by skill (query param: skill=)",
		Tags:    []string{"registry"},
		Output:  models.SearchAgentsResponse{},
		Params:  []schema.ParamSchema{{Name: "skill", In: "query", Required: false, Type: "string"}},
	}).Handle(controllers.SearchAgents)

	reg.Schema(schema.Route{
		Method:  "DELETE",
		Path:    "/agents/:id",
		Summary: "Deregister an agent",
		Tags:    []string{"registry"},
		Params:  []schema.ParamSchema{{Name: "id", In: "path", Required: true, Type: "string"}},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "deleted"},
			404: {Description: "not found"},
		},
	}).Handle(controllers.Deregister)

	// ── Model catalog（LLM 模型清單 + modalities，供 client 選 model） ──
	reg.Schema(schema.Route{
		Method:  "POST",
		Path:    "/models/sync",
		Summary: "Sync available models from Ollama (/api/tags + /api/show)",
		Tags:    []string{"models"},
		Output:  models.SyncModelsResponse{},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "synced"},
			502: {Description: "ollama unreachable"},
		},
	}).Handle(controllers.SyncModels)

	reg.Schema(schema.Route{
		Method:  "GET",
		Path:    "/models",
		Summary: "List recorded LLM models and their modalities",
		Tags:    []string{"models"},
		Output:  models.ListModelsResponse{},
	}).Handle(controllers.ListModels)

	reg.Schema(schema.Route{
		Method:  "GET",
		Path:    "/models/:name",
		Summary: "Get a recorded model by name",
		Tags:    []string{"models"},
		Output:  models.LLMModel{},
		Params:  []schema.ParamSchema{{Name: "name", In: "path", Required: true, Type: "string"}},
		Responses: map[int]schema.ResponseSchema{
			200: {Description: "ok"},
			404: {Description: "not found"},
		},
	}).Handle(controllers.GetModel)
}
