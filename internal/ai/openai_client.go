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

// OpenAIClient implements AIClient using the OpenAI API.
type OpenAIClient struct {
	httpClient     *http.Client
	apiKey         string
	model          string
	apiURL         string
	promptTemplate *template.Template // Parsed template
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model       string          `json:"model"` // Model ID is required for OpenAI
	Messages    []openAIMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"` // Pointer type
	Stop        []string        `json:"stop,omitempty"`        // Stop sequences
}

type openAIResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"` // Model used
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"` // Pointer if optional
	Error   *openAIError   `json:"error,omitempty"`
}

type openAIChoice struct {
	Index        int           `json:"index"`
	Message      openAIMessage `json:"message"`       // The assistant's response message
	FinishReason string        `json:"finish_reason"` // e.g., "stop", "length", "content_filter"
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIError struct {
	Type    string `json:"type"`
	Code    string `json:"code"` // Can be string or interface{}
	Message string `json:"message"`
	Param   string `json:"param"`
}

// --- End Assumed Structs ---

// NewOpenAIClient creates a new client for OpenAI using configuration.
func NewOpenAIClient(cfg config.OpenAIConfig, globalCfg config.Config) (*OpenAIClient, error) {
	apiKey := cfg.APIKey
	if apiKey == "" {
		return nil, errors.New("OpenAI API key not specified (config: providers.openai.api_key or env: OPENAI_API_KEY)")
	}
	modelName := cfg.Model
	if modelName == "" {
		return nil, errors.New("OpenAI model name not specified in config (providers.openai.model)")
	}

	// --- Parse the template (Standardize path if possible) ---
	// Ensure this path points to the *correct* instruction prompt template
	tmplPath := "prompts/openai/completion.tmpl" // Or e.g., "prompts/instruction.tmpl"
	// Use template.ParseFS for consistency
	parsedTemplate, err := template.ParseFS(promptFS, tmplPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI prompt template '%s': %w", tmplPath, err)
	}
	log.Printf("Parsed OpenAI prompt template: %s", tmplPath)
	// ------------------------

	log.Printf("Initializing OpenAI client: Model=%s, Timeout=%s", modelName, globalCfg.TimeoutDuration)
	return &OpenAIClient{
		httpClient:     &http.Client{Timeout: globalCfg.TimeoutDuration},
		apiKey:         apiKey,
		model:          modelName,
		apiURL:         "https://api.openai.com/v1/chat/completions", // Standard chat completions endpoint
		promptTemplate: parsedTemplate,
	}, nil
}

// GetSuggestion implements the AIClient interface for OpenAI using the template.
func (c *OpenAIClient) GetSuggestion(ctx context.Context, promptData *analyzer.ContextInfo) (string, error) {
	log.Printf("Requesting suggestion from %s...", c.Identify())

	// 1. Execute the template to generate the user prompt content
	var userPromptBuf bytes.Buffer
	// Use Execute() for template parsed with ParseFS
	if err := c.promptTemplate.Execute(&userPromptBuf, promptData); err != nil {
		return "", fmt.Errorf("failed to execute OpenAI prompt template: %w", err)
	}
	userPrompt := userPromptBuf.String() // Contains instructions, context, and <END> instruction

	// Define the system prompt (Can be minimal with detailed user prompt)
	systemPrompt := "Output only code." // Or ""

	// Log prompt details
	log.Printf("[GH][OpenAI] Generated User Prompt Snippet: %.100s...", userPrompt)
	log.Printf("[GH][OpenAI] System Prompt: '%s'", systemPrompt)

	// --- Define Request Parameters ---
	temp := 0.1 // Low temperature
	tempPtr := &temp

	// 2. Create request body using shared OpenAI structs
	apiMessages := []openAIMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt}, // User prompt includes context and <END> instruction
	}
	reqBody := openAIRequest{
		Model:       c.model, // Model ID is required for OpenAI
		Messages:    apiMessages,
		MaxTokens:   60,                // Keep relatively low for completion
		Temperature: tempPtr,           // Use pointer
		Stop:        []string{"<END>"}, // <<< Use the custom stop token >>>
	}
	// ---

	// Log request parameters
	log.Printf("[GH][OpenAI] Stop Tokens: %v", reqBody.Stop)
	log.Printf("[GH][OpenAI] Temperature: %v", *reqBody.Temperature)
	log.Printf("[GH][OpenAI] Max Tokens: %d", reqBody.MaxTokens)

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal OpenAI request: %w", err)
	}

	// 3. Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", c.apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create OpenAI request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey) // OpenAI uses Bearer token auth
	req.Header.Set("Accept", "application/json")

	// 4. Send request
	startTime := time.Now()
	resp, err := c.httpClient.Do(req)
	duration := time.Since(startTime)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			log.Printf("OpenAI request cancelled: %v", err)
			return "", err
		}
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("OpenAI request timed out after %s", duration)
			return "", fmt.Errorf("request timed out: %w", err)
		}
		return "", fmt.Errorf("failed to send request to OpenAI: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("OpenAI request completed in %s with status: %s", duration, resp.Status)

	// 5. Parse Response
	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		log.Printf("Error reading OpenAI response body. Status: %s", resp.Status)
		return "", fmt.Errorf("failed to read OpenAI response body: %w", readErr)
	}

	var apiResp openAIResponse // Use shared OpenAI response struct
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		log.Printf("Failed to decode OpenAI JSON response. Status: %s, Body: %s", resp.Status, string(bodyBytes))
		// Attempt to parse just the error part
		var errDetail struct {
			Error *openAIError `json:"error"`
		}
		_ = json.Unmarshal(bodyBytes, &errDetail)
		if errDetail.Error != nil {
			return "", fmt.Errorf("OpenAI API error (%s): %s", errDetail.Error.Code, errDetail.Error.Message)
		}
		return "", fmt.Errorf("failed to decode OpenAI response body (Status %s)", resp.Status)
	}

	// Check for API errors reported in the response body
	if apiResp.Error != nil {
		log.Printf("OpenAI API Error: Type=%s, Code=%s, Message=%s", apiResp.Error.Type, apiResp.Error.Code, apiResp.Error.Message)
		return "", fmt.Errorf("OpenAI API error (%s): %s", apiResp.Error.Code, apiResp.Error.Message)
	}

	// Check HTTP status code *after* checking structured error
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("OpenAI HTTP Error: Status %s, Body: %s", resp.Status, string(bodyBytes))
		return "", fmt.Errorf("OpenAI request failed with HTTP status: %s", resp.Status)
	}

	// Log usage if available
	if apiResp.Usage != nil {
		log.Printf("OpenAI Usage: Prompt=%d, Completion=%d, Total=%d", apiResp.Usage.PromptTokens, apiResp.Usage.CompletionTokens, apiResp.Usage.TotalTokens)
	}

	// 6. Extract and Clean suggestion
	if len(apiResp.Choices) > 0 {
		choice := apiResp.Choices[0]
		finishReason := choice.FinishReason

		log.Printf("OpenAI finish reason: %s", finishReason)
		if finishReason == "length" {
			log.Printf("Warning: OpenAI completion may have been truncated due to max_tokens limit.")
		}
		if finishReason == "content_filter" {
			log.Printf("Warning: OpenAI completion stopped due to content filter.")
			return "", errors.New("suggestion blocked by OpenAI content filter")
		}
		// Handle other finish reasons if necessary

		rawSuggestion := choice.Message.Content
		log.Printf("[GH][OpenAI] RAW Response from model: %s", rawSuggestion)

		// Use the same cleaning function that handles <END> and fences
		suggestion := cleanSuggestions(rawSuggestion, promptData.LanguageID)

		log.Printf("Received AI suggestion (%d chars, cleaned): %.100s...", len(suggestion), suggestion)
		return suggestion, nil
	}

	log.Printf("No choices received from OpenAI. Body: %s", string(bodyBytes))
	return "", errors.New("no suggestion choices received from OpenAI")
}

// Identify returns the client identifier.
func (c *OpenAIClient) Identify() string {
	return fmt.Sprintf("openai/%s", c.model)
}
