package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml" // Add this dependency
)

// --- Configuration Structures ---

// Config holds the overall application configuration.
type Config struct {
	// General AI settings
	Provider string `toml:"provider"` // e.g., "openai", "azure", "ollama", "anthropic", "gemini"
	Model    string `toml:"model"`    // Default model if not specified per provider
	Timeout  string `toml:"timeout"`  // Default request timeout (e.g., "10s", "15000ms")

	// Provider-specific configurations
	Providers Providers `toml:"providers"`

	// Derived fields (not from TOML)
	TimeoutDuration time.Duration `toml:"-"`
}

// Providers contains settings for each supported AI provider.
type Providers struct {
	OpenAI    OpenAIConfig    `toml:"openai"`
	Azure     AzureConfig     `toml:"azure"`
	Anthropic AnthropicConfig `toml:"anthropic"`
	Gemini    GeminiConfig    `toml:"gemini"`
	Ollama    OllamaConfig    `toml:"ollama"`
}

// OpenAIConfig holds settings specific to OpenAI.
type OpenAIConfig struct {
	APIKey string `toml:"api_key"` // Can also be read from env OPENAI_API_KEY as fallback
	Model  string `toml:"model"`   // Specific model override (e.g., gpt-4o)
}

// AzureConfig holds settings specific to Azure OpenAI.
type AzureConfig struct {
	APIKey       string `toml:"api_key"`       // Can also use env AZURE_OPENAI_KEY
	Endpoint     string `toml:"endpoint"`      // Required, e.g., https://your-resource.openai.azure.com/
	DeploymentID string `toml:"deployment_id"` // Required, deployment name
	APIVersion   string `toml:"api_version"`   // Optional, defaults if empty
	Model        string `toml:"model"`         // Optional: Internal name/override if needed
}

// AnthropicConfig holds settings specific to Anthropic.
type AnthropicConfig struct {
	APIKey     string `toml:"api_key"`     // Can also use env ANTHROPIC_API_KEY
	Model      string `toml:"model"`       // Specific model override (e.g., claude-3-sonnet...)
	APIVersion string `toml:"api_version"` // Optional, defaults if empty (e.g., "2023-06-01")
}

// GeminiConfig holds settings specific to Google Gemini.
type GeminiConfig struct {
	APIKey string `toml:"api_key"` // Can also use env GOOGLE_API_KEY
	Model  string `toml:"model"`   // Specific model override (e.g., gemini-1.5-flash-latest)
}

// OllamaConfig holds settings specific to local Ollama.
type OllamaConfig struct {
	Host  string `toml:"host"`  // Optional, defaults to http://localhost:11434
	Model string `toml:"model"` // Required model available in Ollama (e.g., codellama:7b-instruct)
}

// --- Loading Logic ---

const configAppName = "grasshopper" // Used for config directory name

// Default configuration values.
var defaultConfig = Config{
	Provider: "", // No default provider, must be specified
	Model:    "", // No global default model
	Timeout:  "10s",
	Providers: Providers{
		// Default models can be set here if desired
		OpenAI:    OpenAIConfig{Model: "gpt-4o"},
		Azure:     AzureConfig{APIVersion: "2023-07-01-preview"},
		Anthropic: AnthropicConfig{Model: "claude-3-haiku-20240307", APIVersion: "2023-06-01"},
		Gemini:    GeminiConfig{Model: "gemini-1.5-flash-latest"},
		Ollama:    OllamaConfig{Host: "http://localhost:11434", Model: "codellama:latest"},
	},
}

// LoadConfig loads configuration from a TOML file.
// It looks for the file in the user's config directory (e.g., ~/.config/grasshopper/config.toml).
// It falls back to environment variables for API keys if not found in the file.
// It applies default values for missing fields.
func LoadConfig() (*Config, error) {
	cfg := defaultConfig // Start with defaults

	// Determine config file path
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Printf("Warning: Could not find user config directory: %v. Using defaults and env vars.", err)
	} else {
		configFilePath := filepath.Join(configDir, configAppName, "config.toml")
		log.Printf("Attempting to load configuration from: %s", configFilePath)

		// Read and parse the config file if it exists
		if _, err := os.Stat(configFilePath); err == nil {
			// File exists
			if _, err := toml.DecodeFile(configFilePath, &cfg); err != nil {
				return nil, fmt.Errorf("error decoding config file '%s': %w", configFilePath, err)
			}
			log.Printf("Successfully loaded configuration from %s", configFilePath)
		} else if !os.IsNotExist(err) {
			// Other error accessing file (permissions?)
			return nil, fmt.Errorf("error checking config file '%s': %w", configFilePath, err)
		} else {
			log.Printf("Config file not found at %s. Using defaults and environment variables.", configFilePath)
			// Optionally: Create a default config file here if it doesn't exist?
		}
	}

	// --- Apply Fallbacks and Defaults ---

	// Timeout
	var timeoutErr error
	cfg.TimeoutDuration, timeoutErr = time.ParseDuration(cfg.Timeout)
	if timeoutErr != nil {
		log.Printf("Warning: Invalid timeout value '%s' in config. Using default '10s'. Error: %v", cfg.Timeout, timeoutErr)
		cfg.TimeoutDuration = 10 * time.Second // Fallback default
	}
	if cfg.TimeoutDuration <= 500*time.Millisecond { // Enforce minimum
		log.Printf("Warning: Configured timeout '%s' is very low. Setting to 500ms.", cfg.TimeoutDuration)
		cfg.TimeoutDuration = 500 * time.Millisecond
	}

	// API Key Fallbacks from Environment Variables
	if cfg.Providers.OpenAI.APIKey == "" {
		cfg.Providers.OpenAI.APIKey = os.Getenv("OPENAI_API_KEY")
	}
	if cfg.Providers.Azure.APIKey == "" {
		cfg.Providers.Azure.APIKey = os.Getenv("AZURE_OPENAI_KEY")
	}
	if cfg.Providers.Anthropic.APIKey == "" {
		cfg.Providers.Anthropic.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if cfg.Providers.Gemini.APIKey == "" {
		cfg.Providers.Gemini.APIKey = os.Getenv("GOOGLE_API_KEY")
	}
	// Azure endpoint/deployment required, check if still missing
	if cfg.Provider == "azure" && (cfg.Providers.Azure.Endpoint == "" || cfg.Providers.Azure.DeploymentID == "") {
		if cfg.Providers.Azure.Endpoint == "" {
			cfg.Providers.Azure.Endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
		}
		if cfg.Providers.Azure.DeploymentID == "" {
			cfg.Providers.Azure.DeploymentID = os.Getenv("AZURE_OPENAI_DEPLOYMENT")
		}
		if cfg.Providers.Azure.Endpoint == "" || cfg.Providers.Azure.DeploymentID == "" {
			log.Println("Warning: Azure provider selected, but Endpoint or DeploymentID is missing in config and env vars.")
		}
	}
	// Ollama host fallback
	if cfg.Providers.Ollama.Host == "" {
		cfg.Providers.Ollama.Host = os.Getenv("OLLAMA_HOST")
		if cfg.Providers.Ollama.Host == "" {
			cfg.Providers.Ollama.Host = defaultConfig.Providers.Ollama.Host // Use hardcoded default
		}
	}

	// Apply global default model if provider-specific model is empty
	if cfg.Provider == "openai" && cfg.Providers.OpenAI.Model == "" {
		cfg.Providers.OpenAI.Model = cfg.Model
	}
	if cfg.Provider == "azure" && cfg.Providers.Azure.Model == "" {
		cfg.Providers.Azure.Model = cfg.Model
	}
	if cfg.Provider == "anthropic" && cfg.Providers.Anthropic.Model == "" {
		cfg.Providers.Anthropic.Model = cfg.Model
	}
	if cfg.Provider == "gemini" && cfg.Providers.Gemini.Model == "" {
		cfg.Providers.Gemini.Model = cfg.Model
	}
	if cfg.Provider == "ollama" && cfg.Providers.Ollama.Model == "" {
		cfg.Providers.Ollama.Model = cfg.Model
	}

	// Apply hardcoded defaults if still empty
	if cfg.Provider == "openai" && cfg.Providers.OpenAI.Model == "" {
		cfg.Providers.OpenAI.Model = defaultConfig.Providers.OpenAI.Model
	}
	if cfg.Provider == "anthropic" && cfg.Providers.Anthropic.Model == "" {
		cfg.Providers.Anthropic.Model = defaultConfig.Providers.Anthropic.Model
	}
	if cfg.Provider == "gemini" && cfg.Providers.Gemini.Model == "" {
		cfg.Providers.Gemini.Model = defaultConfig.Providers.Gemini.Model
	}
	if cfg.Provider == "ollama" && cfg.Providers.Ollama.Model == "" {
		cfg.Providers.Ollama.Model = defaultConfig.Providers.Ollama.Model
	}
	// Note: Azure model often defaults to deployment ID

	log.Printf("Final Config Loaded: Provider=%s, Timeout=%s", cfg.Provider, cfg.TimeoutDuration)
	return &cfg, nil
}
