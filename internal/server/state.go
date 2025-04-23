package server

import (
	"bufio"
	"io"
	"sync"
	"time" // Added import

	"github.com/FrancescoCarrabino/grasshopper/internal/ai"
	"github.com/FrancescoCarrabino/grasshopper/internal/lsp"
	"github.com/FrancescoCarrabino/grasshopper/internal/parser"
	sitter "github.com/smacker/go-tree-sitter"
)

// DocumentState remains the same
type DocumentState struct {
	Text       string
	Version    int
	LanguageID string
	Tree       *sitter.Tree
}

// Server holds the state and manages the LSP communication loop.
type Server struct {
	reader      *bufio.Reader // Reader for LSP input
	writer      io.Writer     // Writer for LSP output
	writerMutex sync.Mutex    // For sending responses/notifications

	stateMutex              sync.RWMutex // Protects fields below
	initialized             bool
	shutdown                bool
	documents               map[lsp.DocumentURI]DocumentState
	clientCaps              lsp.ClientCapabilities
	parser                  *parser.Manager
	aiClient                ai.AIClient
	supportsIncrementalSync bool // Added: Track client capability

	// Debouncing state
	debounceTimersMutex sync.Mutex                      // Mutex for the timer map
	debounceTimers      map[lsp.DocumentURI]*time.Timer // Map URI to its active debounce timer
	debounceDuration    time.Duration                   // Configurable debounce delay
}

// isInitialized remains the same
func (s *Server) isInitialized() bool {
	s.stateMutex.RLock()
	defer s.stateMutex.RUnlock()
	return s.initialized
}
