package aiconnection

import (
	"canopy/server/internal/platform"
	"path/filepath"
	"strings"
)

func FromDeployment(c platform.Config) (string, map[string]Config) {
	key := c.AICredentialsKeyFile
	if key == "" {
		key = filepath.Join(c.StorageRoot, ".private", "ai-credentials.key")
	}
	defaults := map[string]Config{}
	if c.VisionBaseURL != "" {
		v := Config{Protocol: OpenAI, Endpoint: strings.TrimRight(c.VisionBaseURL, "/"), Model: c.VisionModel, APIKey: c.VisionAPIKey, DeploymentLocal: c.VisionLocal, SupportsVision: true, ExternalConsent: true, ReasoningEffort: c.VisionReasoningEffort, TokenLimitField: "MAX_TOKENS"}
		defaults[Vision] = v
		defaults[Receipt] = v
		defaults[Garden] = v
	}
	if c.KnowledgeBaseURL != "" {
		defaults[Garden] = Config{Protocol: OpenAI, Endpoint: strings.TrimRight(c.KnowledgeBaseURL, "/"), Model: c.KnowledgeModel, APIKey: c.KnowledgeAPIKey, ExternalConsent: true, ReasoningEffort: c.KnowledgeReasoningEffort, TokenLimitField: "MAX_TOKENS"}
	}
	return key, defaults
}
