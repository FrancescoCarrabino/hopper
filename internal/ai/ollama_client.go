package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"text/template"
	"time"

	"github.com/FrancescoCarrabino/grasshopper/internal/analyzer"
	"github.com/FrancescoCarrabino/grasshopper/internal/config" // Import config
)

// OllamaClient implements AIClient using a local Ollama instance.
type OllamaClient struct {
	httpClient     *http.Client
	model          string             // Model name available in Ollama (e.g., "codellama:7b-instruct")
	apiURL         string             // Full URL to Ollama /api/generate endpoint
	promptTemplate *template.Template // Parsed prompt template
}

// Ollama API request structure (for /api/generate)
type ollamaGenerateRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`            // The main prompt text
	System  string                 `json:"system,omitempty"`  // Optional system prompt
	Stream  *bool                  `json:"stream,omitempty"`  // Set to false for single response
	Options map[string]interface{} `json:"options,omitempty"` // For parameters like temperature, num_predict, stop
}

// Ollama API response structure (non-streaming)
type ollamaGenerateResponse struct {
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	Response  string    `json:"response"` // The generated text
	Done      bool      `json:"done"`
	Error     string    `json:"error,omitempty"` // Ollama errors often appear here
	// Timing/token info if available
	TotalDuration      time.Duration `json:"total_duration"`
	LoadDuration       time.Duration `json:"load_duration"`
	PromptEvalCount    int           `json:"prompt_eval_count"`
	PromptEvalDuration time.Duration `json:"prompt_eval_duration"`
	EvalCount          int           `json:"eval_count"`
	EvalDuration       time.Duration `json:"eval_duration"`
	// Context field might be returned, but we usually ignore it for single completions
	// Context []int `json:"context,omitempty"`
}

// NewOllamaClient creates a new client for a local Ollama instance using config.
func NewOllamaClient(cfg config.OllamaConfig, globalCfg config.Config) (*OllamaClient, error) {
	modelName := cfg.Model
	if modelName == "" {
		return nil, errors.New("Ollama model name must be specified in config (providers.ollama.model)")
	}

	host := cfg.Host
	if host == "" {
		// Host should have a default from config loading now, but check anyway
		log.Println("Warning: Ollama host not explicitly specified, using default from config.")
		host = "http://localhost:11434"
	}
	// Validate host format
	_, err := url.ParseRequestURI(host)
	if err != nil {
		return nil, fmt.Errorf("invalid Ollama host '%s': %w", host, err)
	}
	// Ensure no trailing slash before appending path
	apiBaseURL := strings.TrimSuffix(host, "/")
	apiURL := apiBaseURL + "/api/generate"

	// --- Parse the template ---
	// Assuming promptFS is an embed.FS defined elsewhere containing the template file
	tmplPath := "prompts/ollama/completion.tmpl"
	parsedTemplate, err := template.ParseFS(promptFS, tmplPath) // Use direct ParseFS
	if err != nil {
		return nil, fmt.Errorf("failed to parse Ollama prompt template '%s': %w", tmplPath, err)
	}
	log.Printf("Parsed Ollama prompt template: %s", tmplPath)
	// ------------------------

	log.Printf("Initializing Ollama client: Host=%s, Model=%s, API_URL=%s, Timeout=%s",
		apiBaseURL, modelName, apiURL, globalCfg.TimeoutDuration)

	// Optional: Initial ping to check if Ollama is running
	pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second) // Slightly longer timeout for ping
	defer cancel()
	req, _ := http.NewRequestWithContext(pingCtx, "GET", apiBaseURL, nil) // Use base URL for ping
	_, pingErr := (&http.Client{Timeout: 3 * time.Second}).Do(req)        // Use a temporary client for ping
	if pingErr != nil {
		log.Printf("Warning: Could not ping Ollama host '%s': %v. Ensure Ollama is running and accessible.", apiBaseURL, pingErr)
	} else {
		log.Printf("Successfully pinged Ollama host '%s'", apiBaseURL)
	}

	return &OllamaClient{
		httpClient: &http.Client{
			Timeout: globalCfg.TimeoutDuration, // Use global timeout for actual requests
		},
		model:          modelName,
		apiURL:         apiURL,
		promptTemplate: parsedTemplate,
	}, nil
}

// GetSuggestion implements the AIClient interface for Ollama.
func (c *OllamaClient) GetSuggestion(ctx context.Context, promptData *analyzer.ContextInfo) (string, error) {
	log.Printf("Requesting suggestion from %s...", c.Identify())

	// --- Log Context Data for Template ---
	log.Printf("[GH][Ollama] ContextData for Template - Prefix Len: %d", len(promptData.Prefix))
	log.Printf("[GH][Ollama] ContextData for Template - Suffix Len: %d", len(promptData.Suffix))
	// Only log snippets if needed for debugging, avoid large logs
	// log.Printf("[GH][Ollama] ContextData for Template - Prefix:\n---\n%.100s...\n---", promptData.Prefix)
	// log.Printf("[GH][Ollama] ContextData for Template - Suffix:\n---\n%.100s...\n---", promptData.Suffix)
	log.Printf("[GH][Ollama] ContextData for Template - Imports: %d", len(promptData.Imports))
	// ---

	// 1. Execute the Template
	var promptBuf bytes.Buffer
	// Use Execute() as we used template.ParseFS
	errExecute := c.promptTemplate.Execute(&promptBuf, promptData)
	if errExecute != nil {
		log.Printf("[GH][Ollama] ERROR executing template: %v", errExecute)
		return "", fmt.Errorf("failed to execute Ollama prompt template: %w", errExecute)
	}
	prompt := promptBuf.String()
	log.Printf("[GH][Ollama] Generated Instruction Prompt Snippet: %.100s...", prompt) // Log snippet of final prompt

	// --- Define Request Parameters (Instruction-based) ---
	// Define a relevant system prompt (optional, but can help reinforce)
	systemPrompt := `You are a code completion assistant. Output only the code needed to complete the user's current statement or expression.`

	// 2. Create request body
	stream := false // Request a single response
	requestBody := ollamaGenerateRequest{
		Model:  c.model,
		Prompt: prompt, // This now contains the instruction prompt
		System: systemPrompt,
		Stream: &stream,
		Options: map[string]interface{}{
			"num_predict": 50,  // REDUCE max tokens significantly (e.g., 30-70)
			"temperature": 0.1, // Keep low temperature for predictability
			"stop":        []string{"<END>"},
		},
	}
	// ---

	// Log the full request details before sending
	log.Printf("[GH][Ollama] FULL INSTRUCTION PROMPT being sent:\n---\n%s\n---", prompt)
	log.Printf("[GH][Ollama] System Prompt: '%s'", systemPrompt)
	log.Printf("[GH][Ollama] Stop Tokens: %v", requestBody.Options["stop"])
	log.Printf("[GH][Ollama] Temperature: %v", requestBody.Options["temperature"])
	log.Printf("[GH][Ollama] Num Predict: %v", requestBody.Options["num_predict"])

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal Ollama request: %w", err)
	}

	// 3. Create HTTP Request (Use context passed from handler)
	req, err := http.NewRequestWithContext(ctx, "POST", c.apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create Ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// 4. Send Request
	startTime := time.Now()
	resp, err := c.httpClient.Do(req)
	duration := time.Since(startTime)

	// Handle client-side errors (timeout, connection refused, etc.)
	if err != nil {
		// Check for context cancellation explicitly (e.g., from LSP client)
		if errors.Is(err, context.Canceled) {
			log.Printf("Ollama request cancelled (likely by client): %v", err)
			return "", err // Propagate cancellation
		}
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("Ollama request timed out after %s", duration)
			return "", fmt.Errorf("request timed out: %w", err)
		}
		if ue, ok := err.(*url.Error); ok && strings.Contains(ue.Err.Error(), "connection refused") {
			log.Printf("Error connecting to Ollama at %s: connection refused. Is Ollama running?", c.apiURL)
			return "", fmt.Errorf("cannot connect to Ollama host '%s': %w", c.apiURL, err)
		}
		// Other generic HTTP client errors
		return "", fmt.Errorf("failed to send request to Ollama: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("Ollama request completed in %s with status: %s", duration, resp.Status)

	// 5. Parse Response
	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("failed to read Ollama response body: %w", readErr)
	}

	// Log raw response body for debugging before unmarshalling
	// log.Printf("[GH][Ollama] Raw Response Body: %s", string(bodyBytes)) // Uncomment if needed

	var apiResp ollamaGenerateResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		log.Printf("Failed to decode Ollama JSON response. Status: %s, Body: %s", resp.Status, string(bodyBytes))
		return "", fmt.Errorf("failed to decode Ollama response body: %w", err)
	}

	// Check for errors in response body *after* successful unmarshal
	if apiResp.Error != "" {
		// Handle specific errors like model not found
		if strings.Contains(strings.ToLower(apiResp.Error), "model") && strings.Contains(strings.ToLower(apiResp.Error), "not found") {
			log.Printf("Ollama Error: Model '%s' not found locally. Ensure it's pulled via `ollama pull %s`.", c.model, c.model)
			return "", fmt.Errorf("Ollama model '%s' not found locally", c.model)
		}
		log.Printf("Ollama API Error in response body: %s", apiResp.Error)
		return "", fmt.Errorf("Ollama API error: %s", apiResp.Error)
	}

	// Check HTTP status code *after* checking for Ollama error in body
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("Ollama HTTP Error: Status %s, Body: %s", resp.Status, string(bodyBytes))
		return "", fmt.Errorf("Ollama request failed with status: %s", resp.Status)
	}

	// Log raw response text before cleaning
	log.Printf("[GH][Ollama] RAW Instruction Response from model: %s", apiResp.Response)

	// 6. Extract and Clean suggestion
	if apiResp.Done && apiResp.Response != "" { // Check Done status and non-empty response
		// Use a basic cleaning function for instruction-based prompts
		suggestion := cleanSuggestions(apiResp.Response, promptData.LanguageID) // Pass languageID if needed by cleanBasic
		log.Printf("Received AI suggestion (%d chars, Instruction cleaned): %.100s...", len(suggestion), suggestion)
		// Log token counts if available and needed
		// log.Printf("Ollama Usage: Prompt Eval=%d (%s), Eval=%d (%s)", apiResp.PromptEvalCount, apiResp.PromptEvalDuration, apiResp.EvalCount, apiResp.EvalDuration)
		return suggestion, nil
	}

	// Handle cases where response might be technically successful but empty or not 'done'
	log.Printf("No valid suggestion in Ollama response (Status: %s, Done: %t, Response Empty: %t). Body: %s",
		resp.Status, apiResp.Done, apiResp.Response == "", string(bodyBytes))
	return "", errors.New("no complete suggestion response received from Ollama")
}

// Identify returns the client identifier.
func (c *OllamaClient) Identify() string {
	return fmt.Sprintf("ollama/%s", c.model)
}
