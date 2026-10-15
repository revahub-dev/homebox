// Package config provides the configuration for the application.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ardanlabs/conf/v3"
)

// redactedValue is the sentinel substituted for any sensitive field when the
// configuration is serialized (e.g. via Print). It must not match any plausible
// real value.
const redactedValue = "[REDACTED]"

// redactURLUserinfo returns raw with any password component of an embedded
// userinfo section replaced by the redaction sentinel. The username is left
// visible so operators can still recognize which account is configured. Inputs
// that are not parseable URLs, or that contain no password, are returned
// unchanged.
func redactURLUserinfo(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, hasPassword := u.User.Password(); !hasPassword {
		return raw
	}
	// Plain "REDACTED" is used here (rather than redactedValue) so it survives
	// URL percent-encoding without becoming "%5BREDACTED%5D".
	u.User = url.UserPassword(u.User.Username(), "REDACTED")
	return u.String()
}

const (
	ModeDevelopment = "development"
	ModeProduction  = "production"
)

type Config struct {
	conf.Version
	Mode       string         `yaml:"mode"       conf:"default:development"` // development or production
	Web        WebConfig      `yaml:"web"`
	Storage    Storage        `yaml:"storage"`
	Database   Database       `yaml:"database"`
	Log        LoggerConf     `yaml:"logger"`
	Mailer     MailerConf     `yaml:"mailer"`
	Demo       bool           `yaml:"demo"`
	Debug      DebugConf      `yaml:"debug"`
	Options    Options        `yaml:"options"`
	OIDC       OIDCConf       `yaml:"oidc"`
	LabelMaker LabelMakerConf `yaml:"labelmaker"`
	Thumbnail  Thumbnail      `yaml:"thumbnail"`
	Barcode    BarcodeAPIConf `yaml:"barcode"`
	Otel       OTelConfig     `yaml:"otel"`
	Auth       AuthConfig     `yaml:"auth"`
	Notifier   NotifierConf   `yaml:"notifier"`
	Search     SearchConf     `yaml:"search"`
	// Fork: order import (see FORK_NOTES.md)
	OrderImport OrderImportConf `yaml:"order_import"`
	// Fork: shared AI endpoint and idle-item nudges (see FORK_NOTES.md)
	AI      AIConf      `yaml:"ai"`
	Idle    IdleConf    `yaml:"idle"`
	Capture CaptureConf `yaml:"capture"`
}

// SearchConf selects and configures the free-text search engine. The default
// "database" driver searches directly in SQLite/PostgreSQL and needs no extra
// services. The "meilisearch" driver delegates matching to an external
// Meilisearch instance for typo-tolerant, relevance-ranked search.
type SearchConf struct {
	Driver      string          `yaml:"driver"      conf:"default:database"`
	Meilisearch MeilisearchConf `yaml:"meilisearch"`
}

// MeilisearchConf configures the connection to a Meilisearch instance when
// SearchConf.Driver is "meilisearch".
type MeilisearchConf struct {
	Host   string `yaml:"host"    conf:"default:http://localhost:7700"`
	APIKey string `yaml:"api_key"`
	// Index is the Meilisearch index uid entities are stored in.
	Index string `yaml:"index" conf:"default:homebox_entities"`
	// MaxHits caps how many matching entity IDs a single search retrieves
	// from Meilisearch before the database applies its own filters and
	// pagination.
	MaxHits int64 `yaml:"max_hits" conf:"default:1000"`
}

func (c MeilisearchConf) MarshalJSON() ([]byte, error) {
	type alias MeilisearchConf
	a := alias(c)
	if a.APIKey != "" {
		a.APIKey = redactedValue
	}
	return json.Marshal(a)
}

// Validate enforces secure transport for the Meilisearch connection. The API
// key is sent on every request, so a non-local endpoint reached over plaintext
// http would leak it on the wire. https is always allowed; http is permitted
// only for loopback hosts.
func (c MeilisearchConf) Validate() error {
	if c.Host == "" {
		return errors.New("search.meilisearch.host must not be empty")
	}
	u, err := url.Parse(c.Host)
	if err != nil {
		return fmt.Errorf("search.meilisearch.host is not a valid URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		switch strings.ToLower(u.Hostname()) {
		case "localhost", "127.0.0.1", "::1":
			return nil
		default:
			return fmt.Errorf("search.meilisearch.host uses insecure http for non-local host %q: use https", u.Host)
		}
	default:
		return fmt.Errorf("search.meilisearch.host has unsupported scheme %q: must be http or https", u.Scheme)
	}
}

type Options struct {
	CurrencyConfig       string `yaml:"currencies"`
	Hostname             string `yaml:"hostname"`
	AllowRegistration    bool   `yaml:"disable_registration"    conf:"default:true"`
	AutoIncrementAssetID bool   `yaml:"auto_increment_asset_id" conf:"default:true"`
	GithubReleaseCheck   bool   `yaml:"check_github_release"    conf:"default:true"`
	AllowAnalytics       bool   `yaml:"allow_analytics"         conf:"default:false"`
	AllowLocalLogin      bool   `yaml:"allow_local_login"       conf:"default:true"`
	TrustProxy           bool   `yaml:"trust_proxy"             conf:"default:false"`
}

type Thumbnail struct {
	Enabled bool `yaml:"enabled" conf:"default:true"`
	Width   int  `yaml:"width"   conf:"default:500"`
	Height  int  `yaml:"height"  conf:"default:500"`
}

type DebugConf struct {
	Enabled bool   `yaml:"enabled" conf:"default:false"`
	Port    string `yaml:"port"    conf:"default:4000"`
}

type WebConfig struct {
	Port string `yaml:"port" conf:"default:7745"`
	Host string `yaml:"host"`
	// MaxUploadSize is the body cap (in MB) applied to ordinary upload
	// endpoints (attachments, item imports, etc.). Defaults to 10 MB.
	MaxUploadSize int64 `yaml:"max_file_upload" conf:"default:10"`
	// MaxImportSize is the body cap (in MB) for collection-restore uploads
	// (POST /v1/group/import). Set independently because a full collection
	// backup including attachments can be much larger than a single asset
	// upload. Defaults to 1 GB.
	MaxImportSize int64 `yaml:"max_import_size" conf:"default:1024"`
	// MaxParseMemory is the amount of memory used when parsing multipart form
	// the data that does not fit into this memory will spil to temp files.
	// Defaults to 64 MB.
	MaxParseMemory int64         `yaml:"max_parse_memory" conf:"default:64"`
	ReadTimeout    time.Duration `yaml:"read_timeout"     conf:"default:10s"`
	WriteTimeout   time.Duration `yaml:"write_timeout"    conf:"default:10s"`
	IdleTimeout    time.Duration `yaml:"idle_timeout"     conf:"default:30s"`
}

type LabelMakerConf struct {
	Width                 int64          `yaml:"width"                 conf:"default:526"`
	Height                int64          `yaml:"height"                conf:"default:200"`
	Padding               int64          `yaml:"padding"               conf:"default:32"`
	Margin                int64          `yaml:"margin"                conf:"default:32"`
	FontSize              float64        `yaml:"font_size"             conf:"default:32.0"`
	PrintCommand          *string        `yaml:"string"`
	AdditionalInformation *string        `yaml:"string"`
	DynamicLength         bool           `yaml:"bool"                  conf:"default:true"`
	LabelServiceUrl       *string        `yaml:"label_service_url"`
	LabelServiceTimeout   *time.Duration `yaml:"label_service_timeout"`
	RegularFontPath       *string        `yaml:"regular_font_path"`
	BoldFontPath          *string        `yaml:"bold_font_path"`
}

type OIDCConf struct {
	IssuerURL          string        `yaml:"issuer_url"`
	ClientID           string        `yaml:"client_id"`
	ClientSecret       string        `yaml:"client_secret"`
	Scope              string        `yaml:"scope"                conf:"default:openid profile email"`
	AllowedGroups      string        `yaml:"allowed_groups"`
	GroupClaim         string        `yaml:"group_claim"          conf:"default:groups"`
	EmailClaim         string        `yaml:"email_claim"          conf:"default:email"`
	NameClaim          string        `yaml:"name_claim"           conf:"default:name"`
	EmailVerifiedClaim string        `yaml:"email_verified_claim" conf:"default:email_verified"`
	ButtonText         string        `yaml:"button_text"          conf:"default:Sign in with OIDC"`
	StateExpiry        time.Duration `yaml:"state_expiry"         conf:"default:10m"`
	RequestTimeout     time.Duration `yaml:"request_timeout"      conf:"default:30s"`
	Enabled            bool          `yaml:"enabled"              conf:"default:false"`
	AutoRedirect       bool          `yaml:"auto_redirect"        conf:"default:false"`
	VerifyEmail        bool          `yaml:"verify_email"         conf:"default:false"`
}

func (c OIDCConf) MarshalJSON() ([]byte, error) {
	type alias OIDCConf
	a := alias(c)
	if a.ClientSecret != "" {
		a.ClientSecret = redactedValue
	}
	return json.Marshal(a)
}

type BarcodeAPIConf struct {
	TokenBarcodespider   string `yaml:"token_barcodespider"`
	OpenFoodFactsContact string `yaml:"openfoodfacts_contact"`
}

func (c BarcodeAPIConf) MarshalJSON() ([]byte, error) {
	type alias BarcodeAPIConf
	a := alias(c)
	if a.TokenBarcodespider != "" {
		a.TokenBarcodespider = redactedValue
	}
	return json.Marshal(a)
}

// OrderImportConf configures the fork's order import. Env vars use the
// HBOX_ORDER_IMPORT_ prefix, e.g. HBOX_ORDER_IMPORT_LLM_BASE_URL.
type OrderImportConf struct {
	// InboundDomain is the domain of the forwarding address shown in the UI
	// (orders-<token>@InboundDomain). Mail for it must be delivered to the
	// webhook by an inbound-email service. Empty hides the address.
	InboundDomain string `yaml:"inbound_domain"`
	// LLM fallback / vision extractor. Disabled unless base URL and model are set.
	LLMBaseURL     string `yaml:"llm_base_url"`
	LLMApiKey      string `yaml:"llm_api_key"`
	LLMModel       string `yaml:"llm_model"`
	LLMVisionModel string `yaml:"llm_vision_model"`
	LLMTimeoutSec  int    `yaml:"llm_timeout_sec"  conf:"default:60"`
}

func (c OrderImportConf) MarshalJSON() ([]byte, error) {
	type alias OrderImportConf
	a := alias(c)
	if a.LLMApiKey != "" {
		a.LLMApiKey = redactedValue
	}
	return json.Marshal(a)
}

// AIConf is the fork's shared OpenAI-compatible endpoint (HBOX_AI_*), used by
// photo capture, "What can I build?" and sale-listing drafts. It works with
// cloud APIs (OpenAI and compatibles) and with local servers (Ollama at
// http://host:11434/v1, LM Studio at http://host:1234/v1). Switching between
// them is a matter of changing these variables. When BaseURL/Model are empty
// the order-import LLM settings (HBOX_ORDER_IMPORT_LLM_*) are used instead.
type AIConf struct {
	BaseURL string `yaml:"base_url"`
	// APIKey is optional: local servers usually need none.
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`
	// VisionModel is used for photos; defaults to Model.
	VisionModel string `yaml:"vision_model"`
	// ResponseFormat is how JSON output is requested: "json_object"
	// (default; retried without it if the server rejects it), "json_schema"
	// (LM Studio style) or "none".
	ResponseFormat string `yaml:"response_format" conf:"default:json_object"`
	TimeoutSec     int    `yaml:"timeout_sec"     conf:"default:120"`
}

func (c AIConf) MarshalJSON() ([]byte, error) {
	type alias AIConf
	a := alias(c)
	if a.APIKey != "" {
		a.APIKey = redactedValue
	}
	return json.Marshal(a)
}

// AIResolved returns the effective AI settings, falling back to the
// order-import LLM configuration.
func (c *Config) AIResolved() AIConf {
	a := c.AI
	if a.BaseURL == "" || a.Model == "" {
		a.BaseURL, a.APIKey, a.Model = c.OrderImport.LLMBaseURL, c.OrderImport.LLMApiKey, c.OrderImport.LLMModel
		a.VisionModel = c.OrderImport.LLMVisionModel
	}
	if a.VisionModel == "" {
		a.VisionModel = a.Model
	}
	if a.ResponseFormat == "" {
		a.ResponseFormat = "json_object"
	}
	if a.TimeoutSec <= 0 {
		a.TimeoutSec = 120
	}
	return a
}

// CaptureConf tunes photo capture (HBOX_CAPTURE_*).
type CaptureConf struct {
	// Workers is how many photos are analysed at once. Keep 1 for a local GPU.
	Workers int `yaml:"workers" conf:"default:1"`
	// MaxPhotosPerDay caps analysed photos per collection per day (0 = unlimited).
	MaxPhotosPerDay int `yaml:"max_photos_per_day" conf:"default:300"`
	// MaxPhotoMB is the largest accepted upload after client resize.
	MaxPhotoMB int `yaml:"max_photo_mb" conf:"default:8"`
	// RetentionDays is how long unaccepted photos and crops are kept.
	RetentionDays int `yaml:"retention_days" conf:"default:7"`
	// Language is an optional language code ("ja", "en") for generic item
	// descriptions. Text printed on packaging is always kept as written.
	Language string `yaml:"language"`
}

// IdleConf configures the weekly idle-item digest (HBOX_IDLE_*).
type IdleConf struct {
	Enabled      bool `yaml:"enabled"       conf:"default:true"`
	Days         int  `yaml:"days"          conf:"default:180"`
	DigestLimit  int  `yaml:"digest_limit"  conf:"default:10"`
	SnoozeDays   int  `yaml:"snooze_days"   conf:"default:90"`
	IntervalDays int  `yaml:"interval_days" conf:"default:7"`
}

type AuthConfig struct {
	RateLimit AuthRateLimit `yaml:"rate_limit"`
	// APIKeyPepper is a server-side secret HMAC-keyed into stored API key hashes.
	// Holding it outside the database means a DB-only leak yields no usable hashes.
	// Must stay stable across restarts — rotating it invalidates every issued key.
	// Generate with `openssl rand -base64 48`.
	APIKeyPepper string `yaml:"api_key_pepper" conf:"mask"`
}

func (c AuthConfig) MarshalJSON() ([]byte, error) {
	type alias AuthConfig
	a := alias(c)
	if a.APIKeyPepper != "" {
		a.APIKeyPepper = redactedValue
	}
	return json.Marshal(a)
}

type AuthRateLimit struct {
	Enabled     bool          `yaml:"enabled"      conf:"default:true"`
	Window      time.Duration `yaml:"window"       conf:"default:1m"`
	MaxAttempts int           `yaml:"max_attempts" conf:"default:5"`
	BaseBackoff time.Duration `yaml:"base_backoff" conf:"default:10s"`
	MaxBackoff  time.Duration `yaml:"max_backoff"  conf:"default:5m"`
}

// New parses the CLI/Config file and returns a Config struct. If the file argument is an empty string, the
// file is not read. If the file is not empty, the file is read and the Config struct is returned.
func New(buildstr string, description string) (*Config, error) {
	var cfg Config
	const prefix = "HBOX"

	cfg.Version = conf.Version{
		Build: buildstr,
		Desc:  description,
	}

	help, err := conf.Parse(prefix, &cfg)
	if err != nil {
		if errors.Is(err, conf.ErrHelpWanted) {
			fmt.Println(help)
			os.Exit(0)
		}
		return &cfg, fmt.Errorf("parsing config: %w", err)
	}

	// Only enforce Meilisearch transport rules when that driver is selected;
	// the unused default host is otherwise irrelevant.
	if cfg.Search.Driver == "meilisearch" {
		if err := cfg.Search.Meilisearch.Validate(); err != nil {
			return &cfg, fmt.Errorf("invalid meilisearch config: %w", err)
		}
	}

	return &cfg, nil
}

// Print prints the configuration to stdout as an indented JSON document.
// Sensitive fields (secrets, tokens, passwords, embedded URL credentials) are
// redacted via each sub-struct's MarshalJSON. Useful for debugging operator
// configuration without leaking credentials to logs.
func (c *Config) Print() {
	res, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		panic(err)
	}

	fmt.Println(string(res))
}
