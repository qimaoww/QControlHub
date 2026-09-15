package core

import (
	"time"
)

type Config struct {
	ID          string    `json:"id"`
	OwnerID     string    `json:"owner_id,omitempty"`
	AgentID     string    `json:"agent_id,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Engine      Engine    `json:"engine"`
	Content     string    `json:"content"`
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Deployment struct {
	AgentID       string    `json:"agent_id"`
	Engine        Engine    `json:"engine"`
	ConfigID      string    `json:"config_id"`
	ConfigVersion int       `json:"config_version"`
	DeployedAt    time.Time `json:"deployed_at"`
}

// ConfigTemplate is a reusable configuration body with {{variable}} placeholders
// that are rendered per node when the template is applied.
type ConfigTemplate struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"owner_id,omitempty"`
	Name      string    `json:"name"`
	Engine    Engine    `json:"engine"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
