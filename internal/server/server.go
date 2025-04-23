package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/textproto"
	"strconv"
	"time"

	"github.com/FrancescoCarrabino/grasshopper/internal/ai"     // Import AI package
	"github.com/FrancescoCarrabino/grasshopper/internal/config" // <<< Import Config package
	"github.com/FrancescoCarrabino/grasshopper/internal/lsp"
	"github.com/FrancescoCarrabino/grasshopper/internal/parser"
)

// Server struct definition is in state.go (ensure aiClient ai.AIClient field exists)

// NewServer creates a new LSP server instance and initializes components.
func NewServer() *Server {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("FATAL: Failed to load configuration: %v", err)
	}

	parserManager, err := parser.NewManager()
	if err != nil {
		log.Printf("ERROR initializing parser: %v. Parsing disabled.", err)
		parserManager = nil
	}

	var activeAIClient ai.AIClient
	var aiErr error
	// AI Client initialization based on cfg (same as previous version)...
	switch cfg.Provider {
	case "openai":
		activeAIClient, aiErr = ai.NewOpenAIClient(cfg.Providers.OpenAI, *cfg)
	case "azure":
		activeAIClient, aiErr = ai.NewAzureOpenAIClient(cfg.Providers.Azure, *cfg)
	case "anthropic":
		activeAIClient, aiErr = ai.NewAnthropicClient(cfg.Providers.Anthropic, *cfg)
	case "gemini":
		activeAIClient, aiErr = ai.NewGeminiClient(cfg.Providers.Gemini, *cfg)
	case "ollama":
		activeAIClient, aiErr = ai.NewOllamaClient(cfg.Providers.Ollama, *cfg)
		// ... default/error handling ...
	}
	if aiErr != nil {
		log.Printf("ERROR initializing AI client for provider '%s': %v...", cfg.Provider, aiErr)
		activeAIClient = nil
	}
	// ... logging success ...

	// --- Initialize Debounce ---
	debounceDuration := 300 * time.Millisecond // Default debounce time
	// TODO: Make debounce duration configurable via config.toml
	log.Printf("Using debounce duration: %s", debounceDuration)
	// -------------------------

	return &Server{
		documents:        make(map[lsp.DocumentURI]DocumentState),
		parser:           parserManager,
		aiClient:         activeAIClient,
		debounceTimers:   make(map[lsp.DocumentURI]*time.Timer), // Init timer map
		debounceDuration: debounceDuration,                      // Store duration
	}
}

// Run starts the server's main loop, reading from r and writing to w.
func (s *Server) Run(r io.Reader, w io.Writer) error {
	s.reader = bufio.NewReader(r)
	s.writer = w
	defer s.Close() // Ensure resources are closed on exit

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Cancel context when Run returns

	// Main loop: Read header -> Read content -> Handle message
	for {
		select {
		case <-ctx.Done(): // Check if context was cancelled
			log.Println("Server context cancelled, exiting read loop.")
			return ctx.Err()
		default:
			// Continue reading headers/messages
		}

		// Read Content-Length header
		mimeReader := textproto.NewReader(s.reader)
		header, err := mimeReader.ReadMIMEHeader()
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				log.Println("Client closed connection (EOF on header read)")
				return nil // Graceful exit
			}
			if ctx.Err() != nil {
				return ctx.Err()
			} // Check if shutdown requested during read
			log.Printf("Error reading header: %v", err)
			continue // Try reading next message
		}

		contentLengthStr := header.Get("Content-Length")
		if contentLengthStr == "" {
			log.Println("Error: Missing Content-Length header")
			continue
		}

		contentLength, err := strconv.Atoi(contentLengthStr)
		if err != nil || contentLength < 0 {
			log.Printf("Error converting Content-Length '%s' to int: %v", contentLengthStr, err)
			continue
		}

		// Read the JSON content
		jsonData := make([]byte, contentLength)
		n, err := io.ReadFull(s.reader, jsonData)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				log.Println("Client closed connection (EOF on content read)")
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			} // Check if shutdown requested during read
			log.Printf("Error reading content (read %d/%d bytes): %v", n, contentLength, err)
			continue // Try reading next message
		}

		// Handle the message, passing the cancellable context
		shutdownRequested := s.handleMessage(ctx, jsonData)
		if shutdownRequested {
			log.Println("Exit notification processed, stopping server run loop.")
			cancel()   // Signal context cancellation for any ongoing tasks
			return nil // Exit loop gracefully after 'exit'
		}
	}
}

// handleMessage decodes and dispatches a received JSON message.
// Returns true if the 'exit' notification was received.
func (s *Server) handleMessage(ctx context.Context, jsonData []byte) (shutdownRequested bool) {
	var req lsp.RequestMessage
	if err := json.Unmarshal(jsonData, &req); err != nil {
		log.Printf("Error unmarshalling base request: %v. JSON: %s", err, string(jsonData))
		return false // Don't shut down on parse error
	}

	log.Printf("Received message: Method=%s (ID: %v)", req.Method, req.ID)

	// Dispatch based on method
	var err error
	switch req.Method {
	// --- Handlers Defined in handlers.go ---
	case "initialize":
		err = s.handleInitialize(ctx, req)
	case "initialized":
		err = s.handleInitialized(ctx, req)
	case "shutdown":
		err = s.handleShutdown(ctx, req)
	case "exit":
		s.handleExit(ctx, req)
		return true // Signal Run loop to stop
	case "textDocument/didOpen":
		err = s.handleDidOpen(ctx, req)
	case "textDocument/didChange":
		err = s.handleDidChange(ctx, req)
	case "textDocument/didSave":
		err = s.handleDidSave(ctx, req)
	case "textDocument/didClose":
		err = s.handleDidClose(ctx, req)
	case "textDocument/inlineCompletion":
		err = s.handleInlineCompletion(ctx, req)

	// *** ADD CASE FOR STANDARD COMPLETION ***
	case "textDocument/completion":
		err = s.handleCompletion(ctx, req)
	// --- End Handlers ---

	// Cancellation / Misc
	case "$/cancelRequest":
		log.Printf("Ignoring cancel request for ID %v", req.ID)
	case "$/setTrace":
		log.Println("Ignoring $/setTrace notification")

	default: // Unhandled Method
		if req.ID != nil { // Request
			log.Printf("Unhandled method request: %s", req.Method)
			errResp := lsp.ResponseError{Code: lsp.MethodNotFound, Message: fmt.Sprintf("Method not supported: %s", req.Method)}
			s.sendResponse(*req.ID, nil, &errResp) // Send error response
		} else { // Notification
			log.Printf("Ignoring unhandled notification: %s", req.Method)
		}
	}

	// Log errors returned by handlers
	if err != nil {
		log.Printf("Error handling method %s: %v", req.Method, err)
		// Consider sending InternalError response for requests if handler failed unexpectedly
		// if req.ID != nil && /* check if error is unexpected */ {
		//     errResp := lsp.ResponseError{ Code: lsp.InternalError, Message: fmt.Sprintf("Internal error processing %s: %v", req.Method, err) }
		//     s.sendResponse(*req.ID, nil, &errResp)
		// }
	}

	return false // No shutdown requested by this message by default
}

// sendResponse method remains the same as the previous version
func (s *Server) sendResponse(id int, result interface{}, respErr *lsp.ResponseError) error {
	s.writerMutex.Lock()
	defer s.writerMutex.Unlock()
	// ... (implementation from previous version) ...
	var rawResult json.RawMessage
	var rawError *lsp.ResponseError = respErr
	var err error
	if respErr == nil && result != nil {
		rawResult, err = json.Marshal(result)
		if err != nil {
			log.Printf("Error marshalling result for ID %d: %v", id, err)
			rawResult = nil
			rawError = &lsp.ResponseError{Code: lsp.InternalError, Message: fmt.Sprintf("Failed to marshal result: %v", err)}
		}
	}
	resp := lsp.ResponseMessage{RPCVersion: "2.0", ID: &id, Result: rawResult, Error: rawError}
	respData, err := json.Marshal(resp)
	if err != nil {
		log.Printf("FATAL: Error marshalling response structure for ID %d: %v", id, err)
		return fmt.Errorf("marshal response structure: %w", err)
	}
	_, writeErr := fmt.Fprintf(s.writer, "Content-Length: %d\r\n\r\n%s", len(respData), respData)
	if writeErr != nil {
		log.Printf("Error writing response data for ID %d: %v", id, writeErr)
		return fmt.Errorf("write response data: %w", writeErr)
	}
	return nil
}

// sendNotification method remains the same as the previous version
func (s *Server) sendNotification(method string, params interface{}) error {
	s.writerMutex.Lock()
	defer s.writerMutex.Unlock()
	// ... (implementation from previous version) ...
	var rawParams json.RawMessage
	var err error
	if params != nil {
		rawParams, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal notification params: %w", err)
		}
	}
	req := lsp.RequestMessage{RPCVersion: "2.0", Method: method, Params: rawParams}
	reqData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal notification structure: %w", err)
	}
	_, writeErr := fmt.Fprintf(s.writer, "Content-Length: %d\r\n\r\n%s", len(reqData), reqData)
	if writeErr != nil {
		return fmt.Errorf("write notification data: %w", writeErr)
	}
	return nil
}

// logToClient method remains the same as the previous version
func (s *Server) logToClient(level lsp.MessageType, message string) {
	s.stateMutex.RLock()
	isInit := s.initialized
	s.stateMutex.RUnlock()
	if !isInit {
		log.Printf("INTERNAL LOG (pre-init): %s", message)
		return
	}
	err := s.sendNotification("window/logMessage", lsp.LogMessageParams{Type: level, Message: message})
	if err != nil {
		log.Printf("Error sending log message to client (level %d): %v - Message: %s", level, err, message)
	}
}

// Close cleans up server resources.
func (s *Server) Close() {
	log.Println("Closing server resources...")
	if s.parser != nil {
		s.parser.Close()
		log.Println("Closed parser manager.")
	}

	// Stop any active debounce timers
	s.debounceTimersMutex.Lock()
	log.Printf("Stopping %d active debounce timers...", len(s.debounceTimers))
	for uri, timer := range s.debounceTimers {
		if timer != nil {
			timer.Stop() // Stop the timer
			log.Printf("Stopped debounce timer for %s", uri)
		}
		delete(s.debounceTimers, uri) // Remove from map
	}
	s.debounceTimersMutex.Unlock()
	log.Println("Debounce timers cleared.")

	// TODO: Add AI client cleanup if needed
}
