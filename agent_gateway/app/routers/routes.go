package routers

import (
	"github.com/maoxiaoyue/hypgo/pkg/schema"
	"github.com/maoxiaoyue/hypgo/pkg/server"

	"agent_gatway/app/controllers"
	"agent_gatway/app/models"
)

func Setup(srv *server.Server) {
	r := srv.Router()

	// A2A agent-card endpoint
	r.GET("/.well-known/agent-card.json", controllers.AgentCardHandler)

	// A2A JSON-RPC 2.0 dispatcher
	r.Schema(schema.Route{
		Method:  "POST",
		Path:    "/",
		Summary: "A2A JSON-RPC 2.0 dispatcher",
		Tags:    []string{"a2a"},
		Input:   models.JSONRPCRequest{},
		Output:  models.JSONRPCResponse{},
	}).Handle(controllers.A2ADispatcher)
}
