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
	"net/url" // Required for url.PathEscape/QueryEscape
	"strings"
	"text/template"
	"time"

	"github.com/FrancescoCarrabino/grasshopper/internal/analyzer"
	"github.com/FrancescoCarrabino/grasshopper/internal/config"
)

// AzureOpenAIClient implements AIClient using Azure's OpenAI service.
type AzureOpenAIClient struct {
	httpClient     *http.Client
	apiKey         string
	endpoint       string             // Full Azure endpoint URL
	deploymentID   string             // Deployment name
	apiVersion     string             // API Version
	model          string             // Model name for identification
	promptTemplate *template.Template // Parsed prompt template
}

// --- Assumed Shared Structs (ensure these are defined elsewhere) ---

/* Example definition (ensure fields match API spec):
type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model       string          `json:"model,omitempty"` // Usually omitted for Azure
	Messages    []openAIMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"` // Pointer type
	Stop        []string        `json:"stop,omitempty"`
}

type openAIResponse struct {
	ID      string                  `json:"id"`
	Object  string                  `json:"object"`
	Created int64                   `json:"created"`
	Model   string                  `json:"model"` // Model used by Azure
	Choices []openAIChoice          `json:"choices"`
	Usage   openAIUsage             `json:"usage"`
	Error   *openAIError            `json:"error,omitempty"`
}

type openAIChoice struct {
	Index        int           `json:"index"`
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"` // e.g., "stop", "length"
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
*/

// --- End Assumed Structs ---

// NewAzureOpenAIClient creates a new client for Azure OpenAI using config.
func NewAzureOpenAIClient(cfg config.AzureConfig, globalCfg config.Config) (*AzureOpenAIClient, error) {
	apiKey := cfg.APIKey
	endpoint := cfg.Endpoint
	deployment := cfg.DeploymentID

	if endpoint == "" {
		return nil, errors.New("Azure endpoint not specified")
	}
	if apiKey == "" {
		return nil, errors.New("Azure API key not specified")
	}
	if deployment == "" {
		return nil, errors.New("Azure deployment ID not specified")
	}

	apiVersion := cfg.APIVersion
	if apiVersion == "" {
		apiVersion = "2023-07-01-preview" // Or choose a newer stable default
		log.Printf("Azure API version not set, using default: %s", apiVersion)
	}

	endpoint = strings.TrimSuffix(endpoint, "/")

	// Use specific model name from config if provided, otherwise use deployment name for identification
	modelName := cfg.Model
	if modelName == "" {
		modelName = deployment
	}

	// --- Parse the template (Standardize path if possible) ---
	// Ensure this path points to the *correct* instruction prompt template
	tmplPath := "prompts/azure/completion.tmpl" // Or e.g., "prompts/instruction.tmpl"
	// Use template.ParseFS for consistency
	parsedTemplate, err := template.ParseFS(promptFS, tmplPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Azure OpenAI prompt template '%s': %w", tmplPath, err)
	}
	log.Printf("Parsed Azure OpenAI prompt template: %s", tmplPath)
	// ------------------------

	log.Printf("Initializing Azure OpenAI client: Endpoint=%s, Deployment=%s, ModelID=%s, APIVersion=%s, Timeout=%s",
		endpoint, deployment, modelName, apiVersion, globalCfg.TimeoutDuration)

	return &AzureOpenAIClient{
		httpClient: &http.Client{
			Timeout: globalCfg.TimeoutDuration,
		},
		apiKey:         apiKey,
		endpoint:       endpoint,
		deploymentID:   deployment,
		apiVersion:     apiVersion,
		model:          modelName,
		promptTemplate: parsedTemplate,
	}, nil
}

// GetSuggestion implements the AIClient interface for Azure OpenAI.
func (c *AzureOpenAIClient) GetSuggestion(ctx context.Context, promptData *analyzer.ContextInfo) (string, error) {
	log.Printf("Requesting suggestion from %s...", c.Identify())

	// 1. Execute template to generate the main user prompt content
	var userPromptBuf bytes.Buffer
	// Use Execute() which executes the main template parsed by template.ParseFS
	if err := c.promptTemplate.Execute(&userPromptBuf, promptData); err != nil {
		return "", fmt.Errorf("failed to execute Azure prompt template: %w", err)
	}
	userPrompt := userPromptBuf.String()
	// System prompt can be minimal or empty when using detailed user prompts for instruct models
	systemPrompt := "Output only code." // Or systemPrompt := ""

	// Log prompt details
	log.Printf("[GH][Azure] Generated User Prompt Snippet: %.100s...", userPrompt)
	log.Printf("[GH][Azure] System Prompt: '%s'", systemPrompt)

	// 2. Create Request Body (using shared OpenAI struct definitions)
	apiMessages := []openAIMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt}, // Contains instructions, context, and <END> instruction
	}

	// Set temperature pointer correctly
	temp := 0.1 // Low temperature for predictable completion
	tempPtr := &temp

	reqBody := openAIRequest{
		// Model field is usually omitted for Azure deployments endpoint
		Messages:    apiMessages,
		MaxTokens:   60,                // Keep relatively low for completion
		Temperature: tempPtr,           // Use pointer
		Stop:        []string{"<END>"}, // <<< Use the custom stop token >>>
	}

	// Log request parameters
	log.Printf("[GH][Azure] Stop Tokens: %v", reqBody.Stop)
	log.Printf("[GH][Azure] Temperature: %v", *reqBody.Temperature) // Dereference pointer for logging value
	log.Printf("[GH][Azure] Max Tokens: %d", reqBody.MaxTokens)

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal Azure OpenAI request: %w", err)
	}

	// 3. Construct Azure-specific URL
	// Example: https://{endpoint}/openai/deployments/{deployment-id}/chat/completions?api-version={api-version}
	// Ensure deployment ID and api version are properly escaped for URL path/query
	apiURL := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		c.endpoint, url.PathEscape(c.deploymentID), url.QueryEscape(c.apiVersion))

	// 4. Create HTTP Request
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create Azure OpenAI request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.apiKey) // Azure-specific header

	// 5. Send Request
	startTime := time.Now()
	resp, err := c.httpClient.Do(req)
	duration := time.Since(startTime)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			log.Printf("Azure OpenAI request cancelled: %v", err)
			return "", err
		}
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("Azure OpenAI request timed out after %s", duration)
			return "", fmt.Errorf("request timed out: %w", err)
		}
		// Add more specific network error checks if needed
		return "", fmt.Errorf("failed to send request to Azure OpenAI: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("Azure OpenAI request completed in %s with status: %s", duration, resp.Status)

	// 6. Parse Response
	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("failed to read Azure OpenAI response body: %w", readErr)
	}
	var apiResp openAIResponse // Reuse shared OpenAI response struct
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		log.Printf("Failed to decode Azure OpenAI JSON response. Status: %s, Body: %s", resp.Status, string(bodyBytes))
		return "", fmt.Errorf("failed to decode Azure OpenAI response body: %w", err)
	}

	// Check for API errors within the JSON response body
	if apiResp.Error != nil {
		log.Printf("Azure OpenAI API Error: Type=%s, Code=%v, Message=%s", apiResp.Error.Type, apiResp.Error.Code, apiResp.Error.Message)
		// Provide more context if available (e.g., check for auth errors, rate limits)
		return "", fmt.Errorf("Azure OpenAI API error (%s): %s", apiResp.Error.Code, apiResp.Error.Message)
	}

	// Check HTTP status code *after* checking for JSON error body
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("Azure OpenAI HTTP Error: Status %s, Body: %s", resp.Status, string(bodyBytes))
		// Try to pull out specific Azure error details if possible from bodyBytes
		return "", fmt.Errorf("Azure OpenAI request failed with HTTP status: %s", resp.Status)
	}

	// 7. Extract and Clean Suggestion
	if len(apiResp.Choices) > 0 {
		rawSuggestion := apiResp.Choices[0].Message.Content
		finishReason := apiResp.Choices[0].FinishReason

		log.Printf("[GH][Azure] RAW Response from model: %s (Finish Reason: %s)", rawSuggestion, finishReason)

		// Use a cleaning function that handles <END> token and potential fences
		suggestion := cleanEndTokenAndFences(rawSuggestion, promptData.LanguageID)

		log.Printf("Received AI suggestion (%d chars, cleaned): %.100s...", len(suggestion), suggestion)
		return suggestion, nil
	}

	log.Printf("No choices received from Azure OpenAI. Status: %s, Body: %s", resp.Status, string(bodyBytes))
	return "", errors.New("no suggestion choices received from Azure OpenAI")
}

// Identify returns the client identifier.
func (c *AzureOpenAIClient) Identify() string {
	// Include deployment ID for clarity
	return fmt.Sprintf("azure/%s(dep:%s)", c.model, c.deploymentID)
}

// cleanEndTokenAndFences removes the <END> token and potential markdown fences.
// (This function can be shared between clients if placed in a common utility area)
func cleanEndTokenAndFences(rawResponse string, languageID string) string {
	cleaned := rawResponse // Start with the raw response

	// 1. Remove the specific stop token
	cleaned = strings.TrimSuffix(cleaned, "<END>")

	// 2. Remove potential leading/trailing code block markers
	langIDLower := strings.ToLower(languageID)
	cleaned = strings.TrimPrefix(cleaned, "```"+langIDLower)
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")

	// 3. Trim leading/trailing whitespace AFTER removing other tokens/fences
	cleaned = strings.TrimSpace(cleaned)

	// 4. Optional: Truncate at the first newline if strict single-line is desired
	// if firstNewline := strings.Index(cleaned, "\n"); firstNewline != -1 {
	//     cleaned = cleaned[:firstNewline]
	// }

	return cleaned
}

// Ensure promptFS is defined via embed in this package or a shared one.
// //go:embed prompts/azure/completion.tmpl
// var promptFS embed.FS
