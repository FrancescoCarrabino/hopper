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

// GeminiClient implements AIClient using Google's Gemini API.
type GeminiClient struct {
	httpClient     *http.Client
	apiKey         string
	model          string // e.g., "gemini-1.5-flash-latest"
	apiURL         string // Constructed URL includes model and key
	promptTemplate *template.Template
}

// --- Gemini API Structures (Keep as defined in your original code) ---
type geminiRequest struct {
	Contents         []geminiContent         `json:"contents"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
	SafetySettings   []geminiSafetySetting   `json:"safetySettings,omitempty"`
}
type geminiContent struct {
	Parts []geminiPart `json:"parts"`
	Role  string       `json:"role,omitempty"` // Optional for user, required for model response? Check docs.
}
type geminiPart struct {
	Text string `json:"text"`
}
type geminiGenerationConfig struct {
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`   // Pointer to allow omitting for default
	StopSequences   []string `json:"stopSequences,omitempty"` // Gemini uses "stopSequences"
}
type geminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}
type geminiResponse struct {
	Candidates     []geminiCandidate     `json:"candidates"`
	PromptFeedback *geminiPromptFeedback `json:"promptFeedback,omitempty"` // Pointer if optional
	Error          *geminiError          `json:"error,omitempty"`
}
type geminiCandidate struct {
	Content       *geminiContent       `json:"content"` // Pointer as it might be missing on error/block
	FinishReason  string               `json:"finishReason"`
	SafetyRatings []geminiSafetyRating `json:"safetyRatings"`
}
type geminiPromptFeedback struct {
	SafetyRatings []geminiSafetyRating `json:"safetyRatings"`
}
type geminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"` // e.g., NEGLIGIBLE, LOW, MEDIUM, HIGH
}
type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// ----------------------------------------------------

// NewGeminiClient creates a new client for Google Gemini using configuration.
func NewGeminiClient(cfg config.GeminiConfig, globalCfg config.Config) (*GeminiClient, error) {
	apiKey := cfg.APIKey
	if apiKey == "" {
		return nil, errors.New("Google API key not specified (config: providers.gemini.api_key or env: GOOGLE_API_KEY)")
	}
	modelName := cfg.Model
	if modelName == "" {
		return nil, errors.New("Gemini model name not specified in config (providers.gemini.model)")
	}

	// --- Parse the template (Standardize path if possible) ---
	// Ensure this path points to the *correct* instruction prompt template
	tmplPath := "prompts/gemini/completion.tmpl" // Or e.g., "prompts/instruction.tmpl"
	// Use template.ParseFS for consistency
	parsedTemplate, err := template.ParseFS(promptFS, tmplPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Gemini prompt template '%s': %w", tmplPath, err)
	}
	log.Printf("Parsed Gemini prompt template: %s", tmplPath)
	// ------------------------

	// Construct API URL (v1beta example, check latest stable version if needed)
	// API key is passed in the URL query for this method.
	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", modelName, apiKey)

	log.Printf("Initializing Gemini client: Model=%s, Timeout=%s", modelName, globalCfg.TimeoutDuration)

	return &GeminiClient{
		httpClient: &http.Client{
			Timeout: globalCfg.TimeoutDuration,
		},
		apiKey:         apiKey, // Stored but usually only used in URL here
		model:          modelName,
		apiURL:         apiURL,
		promptTemplate: parsedTemplate,
	}, nil
}

// GetSuggestion implements the AIClient interface for Gemini.
func (c *GeminiClient) GetSuggestion(ctx context.Context, promptData *analyzer.ContextInfo) (string, error) {
	log.Printf("Requesting suggestion from %s...", c.Identify())

	// 1. Execute template to generate the prompt text
	var userPromptBuf bytes.Buffer
	// Use Execute() for template parsed with ParseFS
	if err := c.promptTemplate.Execute(&userPromptBuf, promptData); err != nil {
		return "", fmt.Errorf("failed to execute Gemini prompt template: %w", err)
	}
	userPrompt := userPromptBuf.String() // Contains instructions, context, and <END> instruction

	// Log prompt details
	log.Printf("[GH][Gemini] Generated User Prompt Snippet: %.100s...", userPrompt)
	// Gemini doesn't typically use a separate system prompt field like OpenAI/Azure

	// --- Define Request Parameters ---
	temp := 0.1 // Low temperature
	tempPtr := &temp

	// 2. Create request body for Gemini generateContent API
	apiContents := []geminiContent{
		// Gemini v1/v1beta often just takes the full prompt as a single user part
		{Parts: []geminiPart{{Text: userPrompt}}, Role: "user"},
		// Note: Some Gemini versions/models might support multi-turn context
		// with alternating "user" and "model" roles if needed later.
	}
	reqBody := geminiRequest{
		Contents: apiContents,
		GenerationConfig: &geminiGenerationConfig{
			MaxOutputTokens: 60,                // Keep relatively low for completion
			Temperature:     tempPtr,           // Use pointer
			StopSequences:   []string{"<END>"}, // <<< Use custom stop token (API field name is StopSequences)
		},
		// Define reasonable safety settings
		SafetySettings: []geminiSafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
		},
	}
	// ---

	// Log request parameters
	log.Printf("[GH][Gemini] Stop Sequences: %v", reqBody.GenerationConfig.StopSequences)
	log.Printf("[GH][Gemini] Temperature: %v", *reqBody.GenerationConfig.Temperature)
	log.Printf("[GH][Gemini] Max Output Tokens: %d", reqBody.GenerationConfig.MaxOutputTokens)

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal Gemini request: %w", err)
	}

	// 3. Create HTTP Request
	req, err := http.NewRequestWithContext(ctx, "POST", c.apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create Gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// 4. Send Request
	startTime := time.Now()
	resp, err := c.httpClient.Do(req)
	duration := time.Since(startTime)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			log.Printf("Gemini request cancelled: %v", err)
			return "", err
		}
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("Gemini request timed out after %s", duration)
			return "", fmt.Errorf("request timed out: %w", err)
		}
		return "", fmt.Errorf("failed to send request to Gemini: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("Gemini request completed in %s with status: %s", duration, resp.Status)

	// 5. Parse Response
	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("failed to read Gemini response body: %w", readErr)
	}
	var apiResp geminiResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		// Sometimes Gemini errors aren't in the standard `error` field on non-200 status
		log.Printf("Failed to decode Gemini JSON response. Status: %s, Body: %s", resp.Status, string(bodyBytes))
		// Attempt to parse just the error part for better message
		var errDetail struct {
			Error *geminiError `json:"error"`
		}
		_ = json.Unmarshal(bodyBytes, &errDetail)
		if errDetail.Error != nil {
			return "", fmt.Errorf("Gemini API error (%s): %s", errDetail.Error.Status, errDetail.Error.Message)
		}
		return "", fmt.Errorf("failed to decode Gemini response body (Status %s)", resp.Status)
	}

	// Check for errors in response structure OR non-200 HTTP status
	if apiResp.Error != nil {
		log.Printf("Gemini API Error: Code=%d, Status=%s, Message=%s", apiResp.Error.Code, apiResp.Error.Status, apiResp.Error.Message)
		return "", fmt.Errorf("Gemini API error (%s): %s", apiResp.Error.Status, apiResp.Error.Message)
	}
	// Check HTTP status AFTER checking the structured error, as some 200s might still contain issues (e.g., blocked)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("Gemini HTTP Error: Status %s, Body: %s", resp.Status, string(bodyBytes))
		// Error message might be in apiResp.Error already handled above, or just generic HTTP error
		return "", fmt.Errorf("Gemini request failed with HTTP status: %s", resp.Status)
	}

	// Check for blocking reasons *before* trying to access candidate content
	// Check prompt feedback first
	if apiResp.PromptFeedback != nil {
		for _, rating := range apiResp.PromptFeedback.SafetyRatings {
			// BLOCK_NONE, BLOCK_ONLY_HIGH, BLOCK_MEDIUM_AND_ABOVE, BLOCK_LOW_AND_ABOVE
			// Consider only blocking on MEDIUM or HIGH? Check API docs. Let's block if not NEGLIGIBLE/LOW
			if rating.Probability != "NEGLIGIBLE" && rating.Probability != "LOW" {
				log.Printf("Gemini prompt blocked due to safety rating: Category=%s, Probability=%s", rating.Category, rating.Probability)
				return "", fmt.Errorf("prompt blocked by Gemini safety filter: %s", rating.Category)
			}
		}
	}
	// Check candidate finish reason and safety ratings
	if len(apiResp.Candidates) > 0 {
		candidate := apiResp.Candidates[0]
		if candidate.FinishReason == "SAFETY" {
			log.Println("Gemini completion blocked by safety filter.")
			// Log specific ratings if available
			for _, rating := range candidate.SafetyRatings {
				log.Printf("Completion Safety Rating: Category=%s, Probability=%s", rating.Category, rating.Probability)
				if rating.Probability != "NEGLIGIBLE" && rating.Probability != "LOW" {
					// Return error only if blocked category is not low/negligible
					return "", fmt.Errorf("completion blocked by Gemini safety filter: %s", rating.Category)
				}
			}
			// If all blocked categories were LOW/NEGLIGIBLE, maybe treat as empty suggestion instead of error?
			log.Println("Completion safety block reported, but probabilities were low/negligible.")
			// Fall through to check content, might be empty.
		}
		if candidate.FinishReason == "MAX_TOKENS" {
			log.Println("Warning: Gemini completion truncated due to maxOutputTokens limit.")
		}
		if candidate.FinishReason == "RECITATION" {
			log.Println("Warning: Gemini completion stopped due to potential recitation.")
		}
		// Other reasons: STOP, OTHER, UNKNOWN, UNSPECIFIED
		log.Printf("Gemini finish reason: %s", candidate.FinishReason)
	} else if apiResp.PromptFeedback == nil && apiResp.Error == nil {
		// No candidates, no prompt feedback, no error -> Something unexpected happened
		log.Printf("Gemini response missing candidates and prompt feedback. Body: %s", string(bodyBytes))
		return "", errors.New("invalid response from Gemini: missing candidates")
	}

	// 6. Extract suggestion (Proceed only if not blocked)
	if len(apiResp.Candidates) > 0 {
		candidate := apiResp.Candidates[0]
		// Check if content and parts exist (might be nil if finishReason was SAFETY etc.)
		if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
			rawSuggestion := candidate.Content.Parts[0].Text
			log.Printf("[GH][Gemini] RAW Response from model: %s", rawSuggestion)

			// Use the same cleaning function that handles <END> and fences
			suggestion := cleanSuggestions(rawSuggestion, promptData.LanguageID)

			log.Printf("Received AI suggestion (%d chars, cleaned): %.100s...", len(suggestion), suggestion)
			return suggestion, nil
		} else {
			log.Printf("Gemini candidate received but content/parts are missing. FinishReason: %s", candidate.FinishReason)
			// Return empty string if blocked for safety but content is nil (already logged/handled above)
			if candidate.FinishReason == "SAFETY" {
				return "", nil // Treat safety block (if not erroring above) as empty suggestion
			}
		}
	}

	// If we get here, no valid candidate content was found for other reasons
	log.Printf("No valid candidates or content received from Gemini. Body: %s", string(bodyBytes))
	return "", errors.New("no valid suggestion content received from Gemini")
}

// Identify returns the client identifier.
func (c *GeminiClient) Identify() string {
	return fmt.Sprintf("gemini/%s", c.model)
}
