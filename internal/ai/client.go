// internal/ai/client.go
package ai

import (
	"context"
	"strings" // Keep for potential use in shared helpers like cleanSuggestion

	"github.com/FrancescoCarrabino/grasshopper/internal/analyzer" // Keep if helpers use NodeInfo
)

// AIClient defines the standard interface that all concrete AI client implementations must satisfy.
type AIClient interface {
	GetSuggestion(ctx context.Context, promptData *analyzer.ContextInfo) (string, error)
	Identify() string
}

// --- Optional Shared Helper Functions ---
// You can keep generic helpers here or move them to utils.go

// safeContent extracts content from NodeInfo, returning "" if nil.
// Useful for formatting prompts safely.
func safeContent(ni *analyzer.NodeInfo) string {
	if ni == nil {
		return ""
	}
	return ni.Content
}

// safeContentSuffix tries to get content after the main suffix within the enclosing block.
// NOTE: This helper's logic might need adjustment based on actual prompt strategies.
func safeContentSuffix(enclosingNode *analyzer.NodeInfo, suffix string) string {
	if enclosingNode == nil {
		return ""
	}
	fullContent := enclosingNode.Content
	suffixIndex := strings.LastIndex(fullContent, suffix) // Maybe LastIndex is better?
	if suffixIndex != -1 {
		startIndex := suffixIndex + len(suffix)
		if startIndex < len(fullContent) {
			// Return a limited amount to avoid huge context?
			maxLength := 500 // Example limit
			endIndex := startIndex + maxLength
			if endIndex > len(fullContent) {
				endIndex = len(fullContent)
			}
			return fullContent[startIndex:endIndex]
		}
	}
	return ""
}

func cleanSuggestions(rawResponse string, languageID string) string {
	langIDLower := strings.ToLower(languageID)

	// Remove potential leading fences (with or without lang ID), multiple times if nested?
	cleaned := strings.TrimPrefix(rawResponse, "```"+langIDLower)
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimPrefix(cleaned, "\n") // Remove leading newline if fence was followed by one

	// Trim potential trailing fence
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "<END>")
	// Trim leading/trailing whitespace AFTER removing fences
	cleaned = strings.TrimSpace(cleaned)

	// <<< ADD: Truncate at the first newline >>>
	// This ensures we only get the first line of generated code,
	// effectively enforcing the single-line completion goal post-hoc.
	if firstNewline := strings.Index(cleaned, "\n"); firstNewline != -1 {
		cleaned = cleaned[:firstNewline]
	}

	return cleaned
}
