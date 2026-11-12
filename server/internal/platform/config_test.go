package platform

import (
	"strings"
	"testing"
)

func envOf(kv map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := kv[k]; return v, ok }
}

func TestConfigMatrix(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{"dev defaults ok", map[string]string{"DATABASE_URL": "postgres://postgres:devpw@localhost:55432/canopy?sslmode=disable"}, false},
		{"prod secure ok", map[string]string{
			"CANOPY_ENV":          "prod",
			"DATABASE_URL":        "postgres://app:secret@db.internal:5432/canopy?sslmode=require",
			"CANOPY_ADDR":         "0.0.0.0:8080",
			"CANOPY_STORAGE_ROOT": "/var/lib/canopy/media",
		}, false},
		{"prod relative storage root rejected", map[string]string{
			"CANOPY_ENV":          "prod",
			"DATABASE_URL":        "postgres://app:secret@db.internal:5432/canopy?sslmode=require",
			"CANOPY_STORAGE_ROOT": "data/media",
		}, true},
		{"prod sslmode=disable rejected", map[string]string{
			"CANOPY_ENV":   "prod",
			"DATABASE_URL": "postgres://app:s@db.internal:5432/canopy?sslmode=disable",
		}, true},
		{"prod localhost rejected", map[string]string{
			"CANOPY_ENV":   "prod",
			"DATABASE_URL": "postgres://app:s@localhost:5432/canopy?sslmode=require",
		}, true},
		{"missing DATABASE_URL rejected", map[string]string{}, true},
		{"unknown env rejected", map[string]string{
			"CANOPY_ENV":   "staging",
			"DATABASE_URL": "postgres://x/y",
		}, true},
		{"bad addr rejected", map[string]string{
			"CANOPY_ADDR":  "localhost",
			"DATABASE_URL": "postgres://x/y",
		}, true},
		{"zero port rejected", map[string]string{
			"CANOPY_ADDR":  "localhost:0",
			"DATABASE_URL": "postgres://x/y",
		}, true},
		{"non-postgres URL rejected", map[string]string{
			"DATABASE_URL": "mysql://x/y",
		}, true},
		{"bad duration rejected", map[string]string{
			"CANOPY_SHUTDOWN_TIMEOUT": "-5s",
			"DATABASE_URL":            "postgres://x/y",
		}, true},
		{"bad log level rejected", map[string]string{
			"CANOPY_LOG_LEVEL": "trace",
			"DATABASE_URL":     "postgres://x/y",
		}, true},
		{"prod insecure cookies rejected", map[string]string{
			"CANOPY_ENV":           "prod",
			"DATABASE_URL":         "postgres://app:s@db.internal:5432/canopy?sslmode=require",
			"CANOPY_COOKIE_SECURE": "0",
		}, true},
		{"bad cookie flag rejected", map[string]string{
			"CANOPY_COOKIE_SECURE": "maybe",
			"DATABASE_URL":         "postgres://x/y",
		}, true},
		{"bad trust-proxy flag rejected", map[string]string{
			"CANOPY_TRUST_PROXY": "yes",
			"DATABASE_URL":       "postgres://x/y",
		}, true},
		{"zero login limit rejected", map[string]string{
			"CANOPY_LOGIN_LIMIT": "0",
			"DATABASE_URL":       "postgres://x/y",
		}, true},
		// T29 AI adapter config: empty base URL is honest no-provider mode.
		{"ai no-provider default ok", map[string]string{
			"DATABASE_URL": "postgres://x/y",
		}, false},
		{"ai clean https origin ok", map[string]string{
			"DATABASE_URL":       "postgres://x/y",
			"CANOPY_AI_BASE_URL": "https://ai.example.com",
		}, false},
		{"ai base url with path rejected", map[string]string{
			"DATABASE_URL":       "postgres://x/y",
			"CANOPY_AI_BASE_URL": "https://ai.example.com/v2/chat",
		}, true},
		{"ai base url with credentials rejected", map[string]string{
			"DATABASE_URL":       "postgres://x/y",
			"CANOPY_AI_BASE_URL": "https://key:secret@ai.example.com",
		}, true},
		{"ai plain http in prod rejected", map[string]string{
			"CANOPY_ENV":          "prod",
			"DATABASE_URL":        "postgres://app:s@db.internal:5432/canopy?sslmode=require",
			"CANOPY_STORAGE_ROOT": "/var/lib/canopy/media",
			"CANOPY_AI_BASE_URL":  "http://ai.example.com",
		}, true},
		{"ai zero timeout rejected", map[string]string{
			"DATABASE_URL":       "postgres://x/y",
			"CANOPY_AI_BASE_URL": "https://ai.example.com",
			"CANOPY_AI_TIMEOUT":  "0s",
		}, true},
		{"ai tiny response cap rejected", map[string]string{
			"DATABASE_URL":                 "postgres://x/y",
			"CANOPY_AI_MAX_RESPONSE_BYTES": "512",
		}, true},
		{"ai zero attempt cost rejected", map[string]string{
			"DATABASE_URL":                     "postgres://x/y",
			"CANOPY_AI_ATTEMPT_COST_MICRO_USD": "0",
		}, true},
		{"ai negative month ceiling rejected", map[string]string{
			"DATABASE_URL":                      "postgres://x/y",
			"CANOPY_AI_MONTH_CEILING_MICRO_USD": "-5",
		}, true},
		{"ai zero month ceiling ok (no monetary ceiling)", map[string]string{
			"DATABASE_URL":                      "postgres://x/y",
			"CANOPY_AI_MONTH_CEILING_MICRO_USD": "0",
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfig(envOf(tc.env))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

// Cookie security is fail-closed: prod is always Secure regardless of
// input; dev defaults insecure but can be forced on; the only way to get
// insecure cookies is a non-prod environment.
func TestCookieSecurityMatrix(t *testing.T) {
	postgres := "postgres://app:s@db.internal:5432/canopy?sslmode=require"
	cases := []struct {
		name       string
		env        map[string]string
		wantSecure bool
		wantTrust  bool
	}{
		{"dev defaults insecure", map[string]string{"DATABASE_URL": postgres}, false, false},
		{"dev forced secure", map[string]string{"DATABASE_URL": postgres, "CANOPY_COOKIE_SECURE": "1"}, true, false},
		{"prod always secure", map[string]string{"CANOPY_ENV": "prod", "DATABASE_URL": postgres, "CANOPY_STORAGE_ROOT": "/var/lib/canopy/media"}, true, false},
		{"prod trust proxy", map[string]string{"CANOPY_ENV": "prod", "DATABASE_URL": postgres, "CANOPY_TRUST_PROXY": "1", "CANOPY_STORAGE_ROOT": "/var/lib/canopy/media"}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := LoadConfig(envOf(tc.env))
			if err != nil {
				t.Fatal(err)
			}
			if c.CookieSecure != tc.wantSecure {
				t.Fatalf("CookieSecure = %v, want %v", c.CookieSecure, tc.wantSecure)
			}
			if c.TrustProxyProto != tc.wantTrust {
				t.Fatalf("TrustProxyProto = %v, want %v", c.TrustProxyProto, tc.wantTrust)
			}
		})
	}
}

// T29: the AI adapter defaults are honest and bounded; no-provider mode
// (empty base URL) is the shipped default.
func TestAIConfigDefaults(t *testing.T) {
	c, err := LoadConfig(envOf(map[string]string{"DATABASE_URL": "postgres://x/y"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.AIBaseURL != "" || c.AIAPIKey != "" {
		t.Fatalf("default must be no-provider: %q", c.AIBaseURL)
	}
	if c.AIModel != "configured-text-model" || c.AIPromptVersion != "p1" || c.AIResultSchemaVer != "r1" {
		t.Fatalf("pins: %q %q %q", c.AIModel, c.AIPromptVersion, c.AIResultSchemaVer)
	}
	if c.AITimeout <= 0 || c.AIMaxResponseBytes < 1024 || c.AIAttemptCostMicro <= 0 || c.AIDailyRunLimit <= 0 {
		t.Fatalf("bounds: %+v", c)
	}
	if c.AIMonthCeilingMicro != 5_000_000 {
		t.Fatalf("default month ceiling: %d", c.AIMonthCeilingMicro)
	}
	if c.AIDailyRunLimit != 10 || c.AIAttemptCostMicro != 4000 {
		t.Fatalf("limits: daily=%d attempt=%d", c.AIDailyRunLimit, c.AIAttemptCostMicro)
	}
}

func TestVisionLocalDeploymentFlag(t *testing.T) {
	for _, tc := range []struct {
		name, value             string
		provided, want, invalid bool
	}{
		{name: "external by default"},
		{name: "explicit local", value: "1", provided: true, want: true},
		{name: "explicit external", value: "0", provided: true},
		{name: "invalid local declaration", value: "maybe", provided: true, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"DATABASE_URL": "postgres://postgres:devpw@localhost:55432/canopy?sslmode=disable"}
			if tc.provided {
				env["CANOPY_VISION_LOCAL"] = tc.value
			}
			cfg, err := LoadConfig(envOf(env))
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid vision-local declaration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.VisionLocal != tc.want {
				t.Fatalf("VisionLocal=%v expected %v", cfg.VisionLocal, tc.want)
			}
		})
	}
}

func TestVisionReasoningEffort(t *testing.T) {
	for _, effort := range []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "bogus"} {
		t.Run(effort, func(t *testing.T) {
			cfg, err := LoadConfig(envOf(map[string]string{"DATABASE_URL": "postgres://postgres:devpw@localhost:55432/canopy?sslmode=disable", "CANOPY_VISION_REASONING_EFFORT": effort}))
			if effort == "bogus" {
				if err == nil {
					t.Fatal("invalid effort accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.VisionReasoningEffort != effort {
				t.Fatalf("effort %q expected %q", cfg.VisionReasoningEffort, effort)
			}
		})
	}
}

func TestKnowledgeEndpointConfig(t *testing.T) {
	for _, test := range []struct {
		base, model, reason string
		valid               bool
	}{
		{"https://model.example", "text-model", "none", true},
		{"https://model.example/v1", "text-model", "none", false},
		{"https://model.example", "", "none", false},
		{"https://model.example", "text-model", "invented", false},
		{"https://secret-user:secret-value@model.example", "text-model", "none", false},
	} {
		cfg, err := LoadConfig(envOf(map[string]string{"DATABASE_URL": "postgres://postgres:devpw@localhost:55432/canopy?sslmode=disable", "CANOPY_KNOWLEDGE_BASE_URL": test.base, "CANOPY_KNOWLEDGE_MODEL": test.model, "CANOPY_KNOWLEDGE_REASONING_EFFORT": test.reason}))
		if (err == nil) != test.valid {
			t.Fatalf("endpoint config validity mismatch: %v", err)
		}
		if err != nil && strings.Contains(err.Error(), "secret-value") {
			t.Fatal("configuration error leaked URL credentials")
		}
		if err == nil && cfg.KnowledgeModel != test.model {
			t.Fatal("dedicated model was not retained")
		}
	}
}

func TestMCPDeploymentConfiguration(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://postgres:devpw@localhost:55432/canopy?sslmode=disable", "CANOPY_MCP_ENABLED": "true"}
	for _, tc := range []struct {
		name   string
		values map[string]string
		bad    bool
	}{
		{"local defaults", nil, false},
		{"remote trusted", map[string]string{"CANOPY_MCP_PUBLIC_URL": "https://garden.example.com/mcp", "CANOPY_MCP_ALLOWED_HOSTS": "garden.example.com"}, false},
		{"untrusted endpoint", map[string]string{"CANOPY_MCP_PUBLIC_URL": "https://garden.example.com/mcp"}, true},
		{"wildcard", map[string]string{"CANOPY_MCP_ALLOWED_HOSTS": "*"}, true},
		{"path in host", map[string]string{"CANOPY_MCP_ALLOWED_HOSTS": "localhost:8080/path"}, true},
		{"credentials in URL", map[string]string{"CANOPY_MCP_PUBLIC_URL": "https://user:secret@garden.example.com/mcp"}, true},
		{"token in URL", map[string]string{"CANOPY_MCP_PUBLIC_URL": "http://localhost:8080/mcp?token=secret"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{}
			for k, v := range base {
				values[k] = v
			}
			for k, v := range tc.values {
				values[k] = v
			}
			if _, err := LoadConfig(envOf(values)); (err != nil) != tc.bad {
				t.Fatalf("configuration result: %v", err)
			}
		})
	}
}
