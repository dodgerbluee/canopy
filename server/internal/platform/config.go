package platform

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ocrLangsRe accepts tesseract language tokens: ISO 639-2 codes joined
// by '+', with the optional script suffix (e.g. eng, deu, eng+deu,
// zh_Hans).
var ocrLangsRe = regexp.MustCompile(`^[a-z]{3}(_[A-Za-z]{3,4})?(\+[a-z]{3}(_[A-Za-z]{3,4})?)*$`)

// Config is the process configuration surface. LoadConfig fails closed:
// an invalid or insecure production configuration must stop startup.
type Config struct {
	MCPEnabled      bool
	MCPAllowedHosts string
	MCPPublicURL    string
	Env             string // dev | test | prod
	Addr            string
	DatabaseURL     string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	LogLevel        string // debug | info | warn | error
	// CookieSecure forces Secure on the session cookie. Production is
	// always Secure; an explicit attempt to disable it stops startup.
	CookieSecure bool
	// TrustProxyProto honors X-Forwarded-Proto for Origin checks when a
	// reverse proxy terminates TLS.
	TrustProxyProto bool
	// StorageRoot is the private ObjectStore root for uploaded bytes.
	// Production requires an absolute path; staging and final objects
	// share one filesystem so the rename is atomic.
	StorageRoot          string
	AICredentialsKeyFile string
	// QuotaBytes is the per-workspace storage quota for asset bytes
	// (default 10 GiB, operator-adjustable).
	QuotaBytes int64
	// MediaImage is the pinned codec container the processor sandbox
	// runs in (decode-only: libvips + HEIF decode plugin, no encoders).
	MediaImage string
	// MediaMaxPixels refuses decode work whose header dimensions exceed
	// this budget (decompression-bomb guard, checked pre-decode).
	MediaMaxPixels int64
	// MediaMemory is the per-command cgroup memory limit for the codec
	// sandbox; swap is pinned equal so the cgroup cannot lean on swap.
	MediaMemory string
	// MediaTimeout bounds one asset's processing wall-clock; the sandbox
	// container is killed when it fires.
	MediaTimeout time.Duration
	// LoginLimit is the sliding-window login budget per key (default
	// 10 per 15 min, operator-adjustable; e2e raises it because the
	// suite signs in once per spec against one API process).
	LoginLimit int64
	// WeatherBaseURL is the forecast provider endpoint (T26). Only
	// user-entered location coordinates are ever fetched from it.
	WeatherBaseURL string
	// WeatherTimeout bounds one provider request; a hung forecast API
	// must never stall a worker run.
	WeatherTimeout time.Duration
	// AI analysis adapter (T29). Empty AIBaseURL is no-provider mode:
	// every capability is honestly unavailable and nothing is fetched.
	// The key is env-only — never logged, never returned by any API.
	AIBaseURL string
	AIAPIKey  string
	// AIModel is the configured model name shown in settings (the only
	// provider identity ever exposed to the UI).
	AIModel             string
	AIPromptVersion     string
	AIResultSchemaVer   string
	AITimeout           time.Duration
	AIMaxResponseBytes  int64
	AIAttemptCostMicro  int64 // operator price bound per attempt (micro-USD)
	AIMonthCeilingMicro int64 // UTC-month external-cost ceiling; 0 = none
	AIDailyRunLimit     int64
	// Identification + receipt extraction adapters (T30). Empty
	// PlantNetAPIKey is no-identification-provider mode: honestly
	// unavailable, nothing fetched. Receipt OCR is local (tesseract +
	// poppler in the pinned media sandbox) and needs no provider; the
	// optional text-model structuring reuses the AI adapter.
	PlantNetBaseURL string
	PlantNetAPIKey  string // env-only; never logged, never returned
	OCRLangs        string // tesseract -l value
	OCRMaxPages     int64  // pdftoppm page bound per receipt
	// Vision analysis adapter (T31). Empty VisionBaseURL is no-vision-
	// provider mode: CapVisionAnalyzer stays honestly unavailable and
	// nothing is fetched. When a base URL is set the model must be
	// explicitly configured — the adapter never defaults or invents one.
	VisionBaseURL      string
	VisionAPIKey       string // env-only; never logged, never returned
	VisionModel        string
	VisionTimeout      time.Duration
	VisionMaxRespBytes int64
	VisionMaxImageB    int64 // per-photo bound sent to the provider
	// VisionLocal states the vision endpoint lives inside this
	// deployment (self-hosted). Default false keeps the honest
	// external-data disclosure on the capability surface.
	VisionLocal bool
	// VisionReasoningEffort is an explicit operator knob for the vision
	// endpoint's reasoning effort ("" = send nothing, keep the provider
	// default). Long image prompts can otherwise exhaust max_tokens with
	// a thinking-only reply; the operator sets the value their endpoint
	// supports. The adapter never defaults or invents one.
	VisionReasoningEffort string
	// Dedicated OpenAI-compatible garden chat; otherwise reuse vision chat.
	KnowledgeBaseURL         string
	KnowledgeAPIKey          string
	KnowledgeModel           string
	KnowledgeReasoningEffort string
	// SMTP delivery (T27): empty SMTPHost means the deployment opted
	// out of email — digest intents stay pending and in-app is
	// unaffected. SMTPTo is the single owner recipient; the password
	// is env-only and never logged.
	SMTPHost    string
	SMTPPort    int64
	SMTPUser    string
	SMTPPass    string
	SMTPFrom    string
	SMTPTo      string
	SMTPTimeout time.Duration
}

func LoadConfig(lookup func(string) (string, bool)) (Config, error) {
	c := Config{
		Env:             "dev",
		MCPPublicURL:    "http://localhost:8080/mcp",
		Addr:            ":8080",
		ReadTimeout:     10 * time.Second,
		WriteTimeout:    30 * time.Second,
		IdleTimeout:     90 * time.Second,
		ShutdownTimeout: 10 * time.Second,
		LogLevel:        "info",
		StorageRoot:     "data/media",
		QuotaBytes:      10 << 30, // 10 GiB per workspace (03-contracts)
		MediaImage:      "canopy-media:r2",
		MediaMaxPixels:  50_000_000, // 50MP: 12MP iPhone + headroom, refuses 576MP bombs
		MediaMemory:     "512m",
		MediaTimeout:    120 * time.Second,
		LoginLimit:      10,
		WeatherBaseURL:  "https://api.open-meteo.com",
		WeatherTimeout:  10 * time.Second,
		AIModel:         "configured-text-model",
		AIPromptVersion: "p1",
		// r1 pins the observation/identification/receipt result schemas
		// frozen in the contract SDL (T01.ai).
		AIResultSchemaVer:   "r1",
		AITimeout:           30 * time.Second,
		AIMaxResponseBytes:  64 << 10,
		AIAttemptCostMicro:  4_000,
		AIMonthCeilingMicro: 5_000_000, // $5/month external cost (03 default)
		AIDailyRunLimit:     10,
		PlantNetBaseURL:     "https://my-api.plantnet.org",
		OCRLangs:            "eng",
		OCRMaxPages:         3,
		VisionTimeout:       120 * time.Second,
		VisionMaxRespBytes:  256 << 10,
		VisionMaxImageB:     15 << 20,
		SMTPPort:            587,
		SMTPTimeout:         10 * time.Second,
	}
	var errs []error
	str := func(key string, dst *string) {
		if v, ok := lookup(key); ok {
			*dst = v
		}
	}
	num := func(key string, dst *int64) {
		if v, ok := lookup(key); ok {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				errs = append(errs, fmt.Errorf("%s: expected positive byte count, got %q", key, v))
				return
			}
			*dst = n
		}
	}
	dur := func(key string, dst *time.Duration) {
		if v, ok := lookup(key); ok {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				errs = append(errs, fmt.Errorf("%s: invalid duration %q", key, v))
				return
			}
			*dst = d
		}
	}

	str("CANOPY_ENV", &c.Env)
	str("CANOPY_ADDR", &c.Addr)
	str("DATABASE_URL", &c.DatabaseURL)
	str("CANOPY_LOG_LEVEL", &c.LogLevel)
	str("CANOPY_STORAGE_ROOT", &c.StorageRoot)
	str("CANOPY_MCP_ALLOWED_HOSTS", &c.MCPAllowedHosts)
	str("CANOPY_MCP_PUBLIC_URL", &c.MCPPublicURL)
	if raw, ok := lookup("CANOPY_MCP_ENABLED"); ok {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("CANOPY_MCP_ENABLED: expected a boolean"))
		} else {
			c.MCPEnabled = value
		}
	}
	str("CANOPY_AI_CREDENTIALS_KEY_FILE", &c.AICredentialsKeyFile)
	num("CANOPY_QUOTA_BYTES", &c.QuotaBytes)
	str("CANOPY_MEDIA_IMAGE", &c.MediaImage)
	num("CANOPY_MEDIA_MAX_PIXELS", &c.MediaMaxPixels)
	str("CANOPY_MEDIA_MEMORY", &c.MediaMemory)
	dur("CANOPY_MEDIA_TIMEOUT", &c.MediaTimeout)
	num("CANOPY_LOGIN_LIMIT", &c.LoginLimit)
	str("CANOPY_WEATHER_BASE_URL", &c.WeatherBaseURL)
	dur("CANOPY_WEATHER_TIMEOUT", &c.WeatherTimeout)
	str("CANOPY_AI_BASE_URL", &c.AIBaseURL)
	str("CANOPY_AI_API_KEY", &c.AIAPIKey)
	str("CANOPY_AI_MODEL", &c.AIModel)
	str("CANOPY_AI_PROMPT_VERSION", &c.AIPromptVersion)
	str("CANOPY_AI_RESULT_SCHEMA_VERSION", &c.AIResultSchemaVer)
	dur("CANOPY_AI_TIMEOUT", &c.AITimeout)
	num("CANOPY_AI_MAX_RESPONSE_BYTES", &c.AIMaxResponseBytes)
	str("CANOPY_PLANTNET_BASE_URL", &c.PlantNetBaseURL)
	str("CANOPY_PLANTNET_API_KEY", &c.PlantNetAPIKey)
	str("CANOPY_OCR_LANGS", &c.OCRLangs)
	num("CANOPY_OCR_MAX_PAGES", &c.OCRMaxPages)
	str("CANOPY_VISION_BASE_URL", &c.VisionBaseURL)
	str("CANOPY_VISION_API_KEY", &c.VisionAPIKey)
	str("CANOPY_VISION_MODEL", &c.VisionModel)
	dur("CANOPY_VISION_TIMEOUT", &c.VisionTimeout)
	num("CANOPY_VISION_MAX_RESPONSE_BYTES", &c.VisionMaxRespBytes)
	num("CANOPY_VISION_MAX_IMAGE_BYTES", &c.VisionMaxImageB)
	// Deployment-local vision is an explicit opt-in; anything else keeps
	// the external-data disclosure.
	if v, ok := lookup("CANOPY_VISION_LOCAL"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("CANOPY_VISION_LOCAL: expected a boolean, got %q", v))
		} else {
			c.VisionLocal = b
		}
	}
	// Reasoning effort is an explicit operator knob; empty means "send
	// nothing" and keeps the provider default on the wire.
	str("CANOPY_VISION_REASONING_EFFORT", &c.VisionReasoningEffort)
	str("CANOPY_KNOWLEDGE_BASE_URL", &c.KnowledgeBaseURL)
	str("CANOPY_KNOWLEDGE_API_KEY", &c.KnowledgeAPIKey)
	str("CANOPY_KNOWLEDGE_MODEL", &c.KnowledgeModel)
	str("CANOPY_KNOWLEDGE_REASONING_EFFORT", &c.KnowledgeReasoningEffort)
	num("CANOPY_AI_ATTEMPT_COST_MICRO_USD", &c.AIAttemptCostMicro)
	num("CANOPY_AI_DAILY_RUN_LIMIT", &c.AIDailyRunLimit)
	// Month ceiling is the one money knob that may be 0 (no monetary
	// ceiling); any other value must be positive.
	if v, ok := lookup("CANOPY_AI_MONTH_CEILING_MICRO_USD"); ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			errs = append(errs, fmt.Errorf("CANOPY_AI_MONTH_CEILING_MICRO_USD: expected non-negative micro-USD, got %q", v))
		} else {
			c.AIMonthCeilingMicro = n
		}
	}
	str("CANOPY_SMTP_HOST", &c.SMTPHost)
	num("CANOPY_SMTP_PORT", &c.SMTPPort)
	str("CANOPY_SMTP_USERNAME", &c.SMTPUser)
	str("CANOPY_SMTP_PASSWORD", &c.SMTPPass)
	str("CANOPY_SMTP_FROM", &c.SMTPFrom)
	str("CANOPY_SMTP_TO", &c.SMTPTo)
	dur("CANOPY_SMTP_TIMEOUT", &c.SMTPTimeout)
	dur("CANOPY_READ_TIMEOUT", &c.ReadTimeout)
	dur("CANOPY_WRITE_TIMEOUT", &c.WriteTimeout)
	dur("CANOPY_IDLE_TIMEOUT", &c.IdleTimeout)
	dur("CANOPY_SHUTDOWN_TIMEOUT", &c.ShutdownTimeout)

	// Cookie security is fail-closed: prod is always Secure, and an
	// explicit CANOPY_COOKIE_SECURE=0 in prod stops startup. dev/test
	// default to insecure (local HTTP exception, 02-architecture §98).
	cookieSecure := ""
	if v, ok := lookup("CANOPY_COOKIE_SECURE"); ok {
		cookieSecure = v
	}
	switch cookieSecure {
	case "", "1", "0":
	default:
		errs = append(errs, fmt.Errorf("CANOPY_COOKIE_SECURE: expected 0 or 1, got %q", cookieSecure))
	}
	c.CookieSecure = c.Env == "prod" || cookieSecure == "1"
	if v, ok := lookup("CANOPY_TRUST_PROXY"); ok {
		switch v {
		case "1":
			c.TrustProxyProto = true
		case "0":
		default:
			errs = append(errs, fmt.Errorf("CANOPY_TRUST_PROXY: expected 0 or 1, got %q", v))
		}
	}

	switch c.Env {
	case "dev", "test", "prod":
	default:
		errs = append(errs, fmt.Errorf("CANOPY_ENV: unknown environment %q", c.Env))
	}
	if c.MCPEnabled {
		endpoint, err := url.Parse(c.MCPPublicURL)
		if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Path != "/mcp" || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
			errs = append(errs, errors.New("CANOPY_MCP_PUBLIC_URL: must be an absolute /mcp HTTP(S) URL without credentials, query or fragment"))
		}
		if c.MCPAllowedHosts == "" {
			if c.Env == "prod" {
				errs = append(errs, errors.New("prod: CANOPY_MCP_ALLOWED_HOSTS must explicitly list trusted authorities"))
			}
			c.MCPAllowedHosts = "localhost:8080,127.0.0.1:8080,host.docker.internal:8080"
		}
		trusted := false
		for _, host := range strings.Split(c.MCPAllowedHosts, ",") {
			host = strings.TrimSpace(host)
			authority, parseErr := url.Parse("https://" + host)
			if host == "" || parseErr != nil || authority.Host != host || authority.Hostname() == "" || authority.User != nil || authority.Path != "" || authority.RawQuery != "" || authority.Fragment != "" || strings.ContainsAny(host, "* \t\r\n") {
				errs = append(errs, errors.New("CANOPY_MCP_ALLOWED_HOSTS: expected exact host or host:port authorities"))
			}
			if endpoint != nil && strings.EqualFold(host, endpoint.Host) {
				trusted = true
			}
		}
		if !trusted {
			errs = append(errs, errors.New("CANOPY_MCP_PUBLIC_URL: authority must be in CANOPY_MCP_ALLOWED_HOSTS"))
		}
		if c.Env == "prod" && (endpoint == nil || endpoint.Scheme != "https") {
			errs = append(errs, errors.New("prod: CANOPY_MCP_PUBLIC_URL must use HTTPS"))
		}
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("CANOPY_LOG_LEVEL: unknown level %q", c.LogLevel))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL: required"))
	} else if u, err := url.Parse(c.DatabaseURL); err != nil ||
		(u.Scheme != "postgres" && u.Scheme != "postgresql") {
		errs = append(errs, errors.New("DATABASE_URL: must be a postgres URL"))
	}
	if _, port, err := net.SplitHostPort(c.Addr); err != nil {
		errs = append(errs, fmt.Errorf("CANOPY_ADDR: invalid %q", c.Addr))
	} else if port == "0" || port == "" {
		errs = append(errs, fmt.Errorf("CANOPY_ADDR: port required"))
	}

	if c.StorageRoot == "" {
		errs = append(errs, errors.New("CANOPY_STORAGE_ROOT: must not be empty"))
	}
	if c.QuotaBytes <= 0 {
		errs = append(errs, errors.New("CANOPY_QUOTA_BYTES: must be positive"))
	}
	if c.MediaImage == "" {
		errs = append(errs, errors.New("CANOPY_MEDIA_IMAGE: must not be empty"))
	}
	if c.MediaMemory == "" {
		errs = append(errs, errors.New("CANOPY_MEDIA_MEMORY: must not be empty"))
	}
	// AI adapter: empty base URL is a valid no-provider mode; a
	// configured one must be a clean http(s) origin (the adapter
	// appends /v1/analyze itself, so a path here is a misconfig).
	if c.AIBaseURL != "" {
		u, err := url.Parse(c.AIBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			(u.Path != "" && u.Path != "/") || u.User != nil || u.RawQuery != "" {
			errs = append(errs, fmt.Errorf("CANOPY_AI_BASE_URL: must be an http(s) origin without path/query/credentials, got %q", c.AIBaseURL))
		}
	}
	if c.AITimeout <= 0 {
		errs = append(errs, errors.New("CANOPY_AI_TIMEOUT: must be positive"))
	}
	if c.AIMaxResponseBytes < 1024 {
		errs = append(errs, errors.New("CANOPY_AI_MAX_RESPONSE_BYTES: must be at least 1024"))
	}
	if c.AIAttemptCostMicro <= 0 {
		errs = append(errs, errors.New("CANOPY_AI_ATTEMPT_COST_MICRO_USD: must be positive"))
	}
	if c.AIDailyRunLimit <= 0 {
		errs = append(errs, errors.New("CANOPY_AI_DAILY_RUN_LIMIT: must be positive"))
	}
	// T30: PlantNet is opt-in by key; when a key is present the pinned
	// origin must be clean. OCR langs must be a tesseract language
	// token — argv is passed directly (no shell), but a strict shape
	// keeps garbage from ever reaching the tool.
	if c.PlantNetAPIKey != "" {
		u, err := url.Parse(c.PlantNetBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			(u.Path != "" && u.Path != "/") || u.User != nil || u.RawQuery != "" {
			errs = append(errs, fmt.Errorf("CANOPY_PLANTNET_BASE_URL: must be an http(s) origin without path/query/credentials, got %q", c.PlantNetBaseURL))
		}
	}
	if !ocrLangsRe.MatchString(c.OCRLangs) {
		errs = append(errs, fmt.Errorf("CANOPY_OCR_LANGS: must be a tesseract language token (eng, deu, eng+deu…), got %q", c.OCRLangs))
	}
	// T31: vision is opt-in by base URL; when enabled the origin must be
	// clean and the model explicitly configured (no default model).
	if c.VisionBaseURL != "" {
		u, err := url.Parse(c.VisionBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			(u.Path != "" && u.Path != "/") || u.User != nil || u.RawQuery != "" {
			errs = append(errs, fmt.Errorf("CANOPY_VISION_BASE_URL: must be an http(s) origin without path/query/credentials, got %q", c.VisionBaseURL))
		}
		if strings.TrimSpace(c.VisionModel) == "" {
			errs = append(errs, errors.New("CANOPY_VISION_MODEL: required when CANOPY_VISION_BASE_URL is set"))
		}
		if c.VisionTimeout <= 0 {
			errs = append(errs, errors.New("CANOPY_VISION_TIMEOUT: must be positive"))
		}
		if c.VisionMaxRespBytes < 1024 {
			errs = append(errs, errors.New("CANOPY_VISION_MAX_RESPONSE_BYTES: must be at least 1024"))
		}
		if c.VisionMaxImageB < 1024 {
			errs = append(errs, errors.New("CANOPY_VISION_MAX_IMAGE_BYTES: must be at least 1024"))
		}
	}
	if c.KnowledgeBaseURL != "" {
		u, err := url.Parse(c.KnowledgeBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			errs = append(errs, errors.New("CANOPY_KNOWLEDGE_BASE_URL: must be an http(s) origin without path/query/credentials"))
		}
		if strings.TrimSpace(c.KnowledgeModel) == "" {
			errs = append(errs, errors.New("CANOPY_KNOWLEDGE_MODEL: required with a knowledge endpoint"))
		}
		if c.Env == "prod" && !strings.HasPrefix(c.KnowledgeBaseURL, "https://") {
			errs = append(errs, errors.New("prod: CANOPY_KNOWLEDGE_BASE_URL must use https"))
		}
	}
	switch c.KnowledgeReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh":
	default:
		errs = append(errs, errors.New("CANOPY_KNOWLEDGE_REASONING_EFFORT: unsupported value"))
	}
	// Effort is validated regardless of whether vision is configured, so
	// a typo fails startup rather than silently reaching a provider.
	switch c.VisionReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh":
	default:
		errs = append(errs, fmt.Errorf("CANOPY_VISION_REASONING_EFFORT: must be empty, none, minimal, low, medium, high or xhigh, got %q", c.VisionReasoningEffort))
	}
	if c.OCRMaxPages < 1 || c.OCRMaxPages > 10 {
		errs = append(errs, errors.New("CANOPY_OCR_MAX_PAGES: must be 1..10"))
	}
	if c.Env == "prod" {
		if c.AIBaseURL != "" && !strings.HasPrefix(c.AIBaseURL, "https://") {
			errs = append(errs, errors.New("prod: CANOPY_AI_BASE_URL must use https"))
		}
		if c.PlantNetAPIKey != "" && !strings.HasPrefix(c.PlantNetBaseURL, "https://") {
			errs = append(errs, errors.New("prod: CANOPY_PLANTNET_BASE_URL must use https"))
		}
		// Insecure production config stops startup.
		if cookieSecure == "0" {
			errs = append(errs, errors.New("prod: session cookies must be Secure (CANOPY_COOKIE_SECURE=0 not permitted)"))
		}
		if !strings.HasPrefix(c.StorageRoot, "/") {
			errs = append(errs, errors.New("prod: CANOPY_STORAGE_ROOT must be an absolute path"))
		}
		if strings.Contains(c.DatabaseURL, "sslmode=disable") {
			errs = append(errs, errors.New("prod: DATABASE_URL must not disable TLS (sslmode=disable)"))
		}
		if strings.Contains(c.DatabaseURL, "localhost") || strings.Contains(c.DatabaseURL, "127.0.0.1") {
			errs = append(errs, errors.New("prod: DATABASE_URL must not point at localhost"))
		}
	}
	return c, errors.Join(errs...)
}
