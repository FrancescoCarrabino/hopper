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
	"text/template"
	"time"

	"github.com/FrancescoCarrabino/grasshopper/internal/analyzer"
	"github.com/FrancescoCarrabino/grasshopper/internal/config"
)

// AnthropicClient implements AIClient using the Anthropic Messages API.
type AnthropicClient struct {
	httpClient     *http.Client
	apiKey         string
	model          string
	apiVersion     string // e.g., "2023-06-01"
	apiURL         string
	promptTemplate *template.Template
}

// --- Anthropic API Structures (Messages API v1) ---
// Reference: https://docs.anthropic.com/claude/reference/messages_post

type anthropicRequest struct {
	Model         string             `json:"model"`
	Messages      []anthropicMessage `json:"messages"`
	System        string             `json:"system,omitempty"`         // Optional system prompt
	MaxTokens     int                `json:"max_tokens"`               // Max tokens to generate (required)
	StopSequences []string           `json:"stop_sequences,omitempty"` // Sequences to stop generation
	Temperature   *float64           `json:"temperature,omitempty"`    // Use pointer for optionality (0.0-1.0)
	// Add other parameters like top_p, top_k if needed
}

type anthropicMessage struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"` // Single string content for basic text
	// For complex content (e.g., images), Content can be an array of objects. We use string for simplicity.
}

// Anthropic API response structure (Messages API v1)
type anthropicResponse struct {
	ID      string     `json:"id"`   // Unique ID for the message.
	Type    string     `json:"type"` // e.g., "message"
	Role    string     `json:"role"` // Should be "assistant"
	Content []struct { // Content is always an array
		Type string `json:"type"` // Should be "text" for our use case
		Text string `json:"text"` // The actual completion
	} `json:"content"`
	Model        string `json:"model"`         // Model that handled the request.
	StopReason   string `json:"stop_reason"`   // e.g., "end_turn", "max_tokens", "stop_sequence"
	StopSequence string `json:"stop_sequence"` // The stop sequence that was hit, if any.
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	// Anthropic Error structure
	Error *anthropicError `json:"error,omitempty"`
}

type anthropicError struct {
	Type    string `json:"type"` // e.g. "error"
	Message string `json:"message"`
}

// --- End API Structures ---

// NewAnthropicClient creates a new client for Anthropic using configuration.
func NewAnthropicClient(cfg config.AnthropicConfig, globalCfg config.Config) (*AnthropicClient, error) {
	apiKey := cfg.APIKey
	if apiKey == "" {
		return nil, errors.New("Anthropic API key not specified (config: providers.anthropic.api_key or env: ANTHROPIC_API_KEY)")
	}
	modelName := cfg.Model
	if modelName == "" {
		return nil, errors.New("Anthropic model name not specified in config (providers.anthropic.model)")
	}
	apiVersion := cfg.APIVersion
	if apiVersion == "" {
		apiVersion = "2023-06-01" // Use a known stable version
		log.Printf("Anthropic API version not set, using default: %s", apiVersion)
	}

	// --- Parse the template (Standardize path if possible) ---
	// Ensure this path points to the *correct* instruction prompt template
	tmplPath := "prompts/anthropic/completion.tmpl" // Or e.g., "prompts/instruction.tmpl"
	// Use template.ParseFS for consistency
	parsedTemplate, err := template.ParseFS(promptFS, tmplPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic prompt template '%s': %w", tmplPath, err)
	}
	log.Printf("Parsed Anthropic prompt template: %s", tmplPath)
	// ------------------------

	log.Printf("Initializing Anthropic client: Model=%s, APIVersion=%s, Timeout=%s",
		modelName, apiVersion, globalCfg.TimeoutDuration)

	return &AnthropicClient{
		httpClient: &http.Client{
			Timeout: globalCfg.TimeoutDuration,
		},
		apiKey:         apiKey,
		model:          modelName,
		apiVersion:     apiVersion,
		apiURL:         "https://api.anthropic.com/v1/messages", // Correct endpoint for Messages API
		promptTemplate: parsedTemplate,
	}, nil
}

// GetSuggestion implements the AIClient interface for Anthropic.
func (c *AnthropicClient) GetSuggestion(ctx context.Context, promptData *analyzer.ContextInfo) (string, error) {
	log.Printf("Requesting suggestion from %s...", c.Identify())

	// 1. Execute template to generate the user prompt content
	var userPromptBuf bytes.Buffer
	// Use Execute() for template parsed with ParseFS
	if err := c.promptTemplate.Execute(&userPromptBuf, promptData); err != nil {
		return "", fmt.Errorf("failed to execute Anthropic prompt template: %w", err)
	}
	userPrompt := userPromptBuf.String() // Contains instructions, context, and <END> instruction

	// Define the system prompt (Can be minimal with detailed user prompt)
	systemPrompt := "Output only code." // Or ""

	// Log prompt details
	log.Printf("[GH][Anthropic] Generated User Prompt Snippet: %.100s...", userPrompt)
	log.Printf("[GH][Anthropic] System Prompt: '%s'", systemPrompt)

	// --- Define Request Parameters ---
	temp := 0.1 // Low temperature
	tempPtr := &temp

	// 2. Create request body for Anthropic Messages API
	apiMessages := []anthropicMessage{
		// Anthropic Messages API expects the conversation history, ending with the user turn.
		// For simple completion, just the user prompt might suffice.
		{Role: "user", Content: userPrompt},
	}
	reqBody := anthropicRequest{
		Model:         c.model,
		Messages:      apiMessages,
		System:        systemPrompt,      // Use the system prompt field
		MaxTokens:     60,                // Required: Set a reasonable limit
		Temperature:   tempPtr,           // Optional temperature pointer
		StopSequences: []string{"<END>"}, // <<< Use custom stop token >>>
	}
	// ---

	// Log request parameters
	log.Printf("[GH][Anthropic] Stop Sequences: %v", reqBody.StopSequences)
	log.Printf("[GH][Anthropic] Temperature: %v", *reqBody.Temperature)
	log.Printf("[GH][Anthropic] Max Tokens: %d", reqBody.MaxTokens)

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal Anthropic request: %w", err)
	}

	// 3. Create HTTP Request
	req, err := http.NewRequestWithContext(ctx, "POST", c.apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create Anthropic request: %w", err)
	}
	// Set required headers for Anthropic API
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", c.apiVersion)
	req.Header.Set("Accept", "application/json")

	// 4. Send Request
	startTime := time.Now()
	resp, err := c.httpClient.Do(req)
	duration := time.Since(startTime)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			log.Printf("Anthropic request cancelled: %v", err)
			return "", err
		}
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("Anthropic request timed out after %s", duration)
			return "", fmt.Errorf("request timed out: %w", err)
		}
		return "", fmt.Errorf("failed to send request to Anthropic: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("Anthropic request completed in %s with status: %s", duration, resp.Status)

	// 5. Parse Response
	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("failed to read Anthropic response body: %w", readErr)
	}
	var apiResp anthropicResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		log.Printf("Failed to decode Anthropic JSON response. Status: %s, Body: %s", resp.Status, string(bodyBytes))
		// Attempt to parse just the error part
		var errDetail struct {
			Error *anthropicError `json:"error"`
		}
		_ = json.Unmarshal(bodyBytes, &errDetail)
		if errDetail.Error != nil {
			return "", fmt.Errorf("Anthropic API error (%s): %s", errDetail.Error.Type, errDetail.Error.Message)
		}
		return "", fmt.Errorf("failed to decode Anthropic response body (Status %s)", resp.Status)
	}

	// Check for API errors reported in the response body
	if apiResp.Error != nil {
		log.Printf("Anthropic API Error: Type=%s, Message=%s", apiResp.Error.Type, apiResp.Error.Message)
		return "", fmt.Errorf("Anthropic API error (%s): %s", apiResp.Error.Type, apiResp.Error.Message)
	}

	// Check HTTP status code *after* checking structured error
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("Anthropic HTTP Error: Status %s, Body: %s", resp.Status, string(bodyBytes))
		// Error details might already be logged above if apiResp.Error was populated
		return "", fmt.Errorf("Anthropic request failed with HTTP status: %s", resp.Status)
	}

	// Log usage and stop reason
	log.Printf("Anthropic Usage: Input=%d, Output=%d", apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens)
	log.Printf("Anthropic stop reason: %s", apiResp.StopReason)
	if apiResp.StopReason == "max_tokens" {
		log.Println("Warning: Anthropic completion likely truncated due to max_tokens limit.")
	}
	if apiResp.StopReason == "stop_sequence" {
		log.Printf("Anthropic stopped due to sequence: %s", apiResp.StopSequence)
	}

	// 6. Extract suggestion
	// Content is an array, usually contains one text block for simple requests
	if len(apiResp.Content) > 0 && apiResp.Content[0].Type == "text" {
		rawSuggestion := apiResp.Content[0].Text
		log.Printf("[GH][Anthropic] RAW Response from model: %s", rawSuggestion)

		// Use the same cleaning function that handles <END> and fences
		suggestion := cleanSuggestions(rawSuggestion, promptData.LanguageID)

		log.Printf("Received AI suggestion (%d chars, cleaned): %.100s...", len(suggestion), suggestion)
		return suggestion, nil
	}

	// Handle cases where response is successful but content array is empty or has wrong type
	log.Printf("No valid text content received from Anthropic. Response Body: %s", string(bodyBytes))
	return "", errors.New("no valid suggestion content received from Anthropic")
}

// Identify returns the client identifier.
func (c *AnthropicClient) Identify() string {
	return fmt.Sprintf("anthropic/%s", c.model)
}
