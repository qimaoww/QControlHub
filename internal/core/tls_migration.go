package core

const ActionMigrateTLS Action = "migrate-tls"
const AgentFeatureTLSMigration = "tls-port-migration-v1"

// Only the signed Agent lease contains this binding. Paths are resolved
// against the Agent's own protected deployment manifest.
type TLSMigrationRoute struct {
	Engine             Engine `json:"engine"`
	InstanceID         string `json:"instance_id"`
	Tag                string `json:"tag"`
	Path               string `json:"path"`
	Domain             string `json:"domain"`
	Transport          string `json:"transport"`
	BackendPort        int    `json:"backend_port"`
	GrantGeneration    int64  `json:"grant_generation,omitempty"`
	CertificateID      string `json:"certificate_id"`
	CertificateVersion int    `json:"certificate_version"`
	From               string `json:"from"`
}
type TLSMigration struct {
	ID     string              `json:"id"`
	Port   int                 `json:"port"`
	Mode   string              `json:"mode"`
	Routes []TLSMigrationRoute `json:"routes"`
}
type TLSRouteVerification struct {
	Candidate string `json:"candidate"`
	Recovery  string `json:"recovery"`
}
type TLSMigrationResult struct {
	State string                 `json:"state"`
	Step  string                 `json:"step"`
	Paths []TLSRouteVerification `json:"paths"`
}
