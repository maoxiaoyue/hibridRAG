package services

import (
	appconfig "agent_gatway/app/config"
	"agent_gatway/app/models"
)

// BuildAgentCard constructs the gateway's own A2A agent card from config
func BuildAgentCard(cfg *appconfig.Config) models.AgentCard {
	return models.AgentCard{
		Name:        cfg.Gateway.Name,
		Description: cfg.Gateway.Description,
		Version:     cfg.Gateway.Version,
		URL:         cfg.Gateway.PublicURL + "/",
		Capabilities: models.A2ACapabilities{
			Streaming:         false,
			PushNotifications: false,
		},
		Skills: []models.A2ASkill{
			{
				ID:          "gateway",
				Name:        "Gateway Routing",
				Description: "Routes A2A messages to the appropriate downstream agent based on skill discovery",
			},
		},
	}
}
