package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time" // Added for debouncing

	sitter "github.com/smacker/go-tree-sitter"
	// Ensure correct import paths for your project structure
	"github.com/FrancescoCarrabino/grasshopper/internal/analyzer"
	"github.com/FrancescoCarrabino/grasshopper/internal/lsp"
	"github.com/FrancescoCarrabino/grasshopper/internal/position"
)

// --- Lifecycle Handlers ---

// handleInitialize responds to the 'initialize' request.
func (s *Server) handleInitialize(ctx context.Context, req lsp.RequestMessage) error {
	if req.ID == nil {
		return errors.New("initialize request missing ID")
	}

	s.stateMutex.Lock()
	if s.initialized {
		s.stateMutex.Unlock()
		log.Println("Warning: Received initialize request after server already initialized.")
		errResp := lsp.ResponseError{Code: -32002, Message: "Server already initialized"}
		return s.sendResponse(*req.ID, nil, &errResp)
	}
	s.stateMutex.Unlock() // Unlock before processing params

	var params lsp.InitializeParams
	if err := json.Unmarshal(req.Params, &params); err != nil { // Use req.Params
		errResp := lsp.ResponseError{
			Code:    lsp.InvalidParams,
			Message: fmt.Sprintf("Failed to unmarshal initialize params: %v", err),
		}
		return s.sendResponse(*req.ID, nil, &errResp)
	}

	// --- Store Client Capabilities ---
	// We don't need to check sync capabilities anymore, as we force Full sync
	s.stateMutex.Lock()
	s.clientCaps = params.Capabilities
	s.stateMutex.Unlock()
	// -------------------------------

	if params.ClientInfo != nil {
		log.Printf("Client Info: Name=%s, Version=%s", params.ClientInfo.Name, params.ClientInfo.Version)
	} else {
		log.Println("Client Info: Not provided")
	}

	// --- Define Server Capabilities (Forcing Full Sync) ---
	openClose := true
	syncKind := lsp.SyncFull // <<< ALWAYS Advertise Full Synchronization
	var resolveProviderFalse = false

	completionOptions := &lsp.CompletionOptions{
		// Optional: Specify characters that trigger completion automatically
		// TriggerCharacters: []string{".", "(", "="}, // Example triggers
		ResolveProvider: &resolveProviderFalse, // Set to false: We don't support resolving additional details later
	}

	result := lsp.InitializeResult{
		Capabilities: lsp.ServerCapabilities{
			TextDocumentSync: &lsp.TextDocumentSyncOptions{
				OpenClose: &openClose,
				Change:    &syncKind, // <<< Announce ONLY Full sync
			},
			// Use standard CompletionProvider for pop-up menu completions
			CompletionProvider: completionOptions,
			// Announce inline completion capability if you implement handleInlineCompletion
			InlineCompletionProvider: &lsp.InlineCompletionOptions{}, // Keep this if you want inline suggestions too
		},
		ServerInfo: &lsp.ServerInfo{
			Name:    "Grasshopper LSP",
			Version: "0.0.1",
		},
	}
	// ----------------------------------------------------

	return s.sendResponse(*req.ID, result, nil)
}

// handleInitialized handles the 'initialized' notification.
func (s *Server) handleInitialized(ctx context.Context, req lsp.RequestMessage) error {
	s.stateMutex.Lock()
	s.initialized = true
	s.stateMutex.Unlock()
	log.Println("Server initialized by client.")
	s.logToClient(lsp.TypeInfo, "Grasshopper LSP server connection initialized.")
	return nil
}

// handleShutdown responds to the 'shutdown' request.
func (s *Server) handleShutdown(ctx context.Context, req lsp.RequestMessage) error {
	log.Println("Shutdown request received.")
	s.stateMutex.Lock()
	if s.shutdown {
		s.stateMutex.Unlock()
		log.Println("Warning: Received shutdown request after already shut down.")
		if req.ID != nil {
			return s.sendResponse(*req.ID, nil, nil)
		}
		return nil
	}
	s.shutdown = true
	s.stateMutex.Unlock()

	// Acknowledge shutdown to client
	if req.ID != nil {
		err := s.sendResponse(*req.ID, nil, nil)
		if err != nil {
			log.Printf("Error sending shutdown response: %v", err)
		}
	} else {
		log.Println("Warning: Shutdown received as notification")
	}

	// Actual cleanup happens in s.Close() called by Run loop exit
	return nil
}

// handleExit handles the 'exit' notification.
// The actual exit/cleanup is managed by the Run loop checking shutdown status.
func (s *Server) handleExit(ctx context.Context, req lsp.RequestMessage) error {
	log.Println("Exit notification received.")
	// No action needed here, Run loop handles termination.
	return nil
}

// --- Document Synchronization Handlers ---

// handleDidOpen handles 'textDocument/didOpen' notifications.
func (s *Server) handleDidOpen(ctx context.Context, req lsp.RequestMessage) error {
	if !s.isInitialized() {
		return errors.New("received didOpen before initialized")
	}

	var params lsp.DidOpenTextDocumentParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.Printf("Error unmarshalling didOpen params: %v", err)
		return nil // Ignore bad notification
	}

	docURI := params.TextDocument.URI
	docText := params.TextDocument.Text
	docLang := params.TextDocument.LanguageID
	docVersion := params.TextDocument.Version

	// Store initial state (without tree first)
	s.stateMutex.Lock()
	s.documents[docURI] = DocumentState{
		Text: docText, Version: docVersion, LanguageID: docLang, Tree: nil,
	}
	s.stateMutex.Unlock()
	log.Printf("Opened document (state stored): %s (Lang: %s, Version: %d, Size: %d)", docURI, docLang, docVersion, len(docText))

	// Trigger initial parse immediately (can also be debounced/async if preferred)
	s.parseDocument(ctx, docURI, docLang, []byte(docText), nil) // Pass nil oldTree for initial parse

	return nil
}

// handleDidSave handles 'textDocument/didSave' notifications (optional).
func (s *Server) handleDidSave(ctx context.Context, req lsp.RequestMessage) error {
	if !s.isInitialized() {
		return errors.New("received didSave before initialized")
	}
	// Can implement logic here if needed (e.g., trigger diagnostics)
	return nil
}

// handleDidClose handles 'textDocument/didClose' notifications.
func (s *Server) handleDidClose(ctx context.Context, req lsp.RequestMessage) error {
	if !s.isInitialized() {
		return errors.New("received didClose before initialized")
	}

	var params lsp.DidCloseTextDocumentParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.Printf("Error unmarshalling didClose params: %v", err)
		return nil
	}
	docURI := params.TextDocument.URI

	// Stop and remove any active debounce timer
	s.debounceTimersMutex.Lock()
	if timer, found := s.debounceTimers[docURI]; found && timer != nil {
		timer.Stop()
		delete(s.debounceTimers, docURI)
		log.Printf("Stopped debounce timer for closing document %s", docURI)
	}
	s.debounceTimersMutex.Unlock()

	// Remove document state
	s.stateMutex.Lock()
	defer s.stateMutex.Unlock()
	if _, ok := s.documents[docURI]; ok {
		delete(s.documents, docURI)
		log.Printf("Closed document state: %s", docURI)
	} else {
		log.Printf("Warning: didClose for unknown document: %s", docURI)
	}
	return nil
}

// handleDidChange handles 'textDocument/didChange' notifications.
// Assumes Full Synchronization is used (advertised in handleInitialize).
func (s *Server) handleDidChange(ctx context.Context, req lsp.RequestMessage) error {
	log.Println("[GH][didChange] START") // Log start

	if !s.isInitialized() {
		log.Println("[GH][didChange] ERROR: Not initialized")
		return errors.New("received didChange before initialized")
	}

	var params lsp.DidChangeTextDocumentParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.Printf("[GH][didChange] ERROR: Unmarshal params error: %v", err)
		return nil // Don't kill server
	}

	docURI := params.TextDocument.URI
	docVersion := params.TextDocument.Version // Make sure to use the version from the notification
	log.Printf("[GH][didChange] Received for URI: %s, Version: %d", docURI, docVersion)

	// --- Apply changes assuming Full Text is sent ---
	s.stateMutex.Lock() // Use Write Lock
	currentState, ok := s.documents[docURI]
	if !ok {
		s.stateMutex.Unlock()
		log.Printf("[GH][didChange] Warning: didChange for unopened document: %s", docURI)
		return nil
	}

	var newText string
	if len(params.ContentChanges) > 0 {
		// <<< FIX: Always take the text from the last change object, assuming it's the full text
		newText = params.ContentChanges[len(params.ContentChanges)-1].Text
		log.Printf("[GH][didChange] Processing full text change (%d bytes received)", len(newText))
	} else {
		s.stateMutex.Unlock()
		log.Println("[GH][didChange] No content changes received.")
		return nil // No changes
	}

	// Store new text/version & keep old tree ref
	oldTree := currentState.Tree
	currentState.Text = newText       // <<< Use the correctly obtained full text
	currentState.Version = docVersion // <<< Update the version number
	s.documents[docURI] = currentState
	log.Printf("[GH][didChange] Updated state in memory (Text Len: %d, Version: %d)", len(currentState.Text), currentState.Version)
	s.stateMutex.Unlock() // Unlock AFTER updating state but before debounce setup
	// -----------------------------------------------------

	//TODO:
	// --- Debounce the Parsing ---
	s.debounceTimersMutex.Lock() // Lock the timer map

	// Stop existing timer (if any) before scheduling a new one
	if oldTimer, found := s.debounceTimers[docURI]; found && oldTimer != nil {
		log.Printf("[GH][Debounce] Stopping existing timer for %s", docURI)
		// Stop prevents the old timer's callback from running if it hasn't started yet.
		// It does NOT guarantee the callback isn't already running.
		oldTimer.Stop()
		// We don't remove it from the map here; the assignment below will overwrite it.
	} else {
		log.Printf("[GH][Debounce] No existing timer found for %s", docURI)
	}

	log.Printf("[GH][Debounce] Scheduling new timer for %s (%s)", docURI, s.debounceDuration)

	// Declare variable for the new timer *before* AfterFunc so the callback closure captures it reliably
	var newScheduledTimer *time.Timer

	// Define the callback function that will execute when the timer fires
	callback := func() {
		// This code runs after debounceDuration has passed without another change
		log.Printf("[GH][Debounce] ---> Timer FIRED for %s <---", docURI)

		parseCtx := context.Background() // Use background context for timer-triggered work

		// --- Get latest state needed for parsing ---
		s.stateMutex.RLock() // Use Read Lock to get document state
		finalState, stillOpen := s.documents[docURI]
		var currentTextBytes []byte
		var currentLangID string
		// Use the 'oldTree' captured from the handleDidChange scope where the timer was scheduled.
		// This 'oldTree' corresponds to the state *before* the change that triggered this timer.
		treeToParseFrom := oldTree
		if stillOpen {
			currentTextBytes = []byte(finalState.Text) // Make copy under lock
			currentLangID = finalState.LanguageID
		}
		s.stateMutex.RUnlock() // Release state lock before potentially long parse
		// --- End state fetching ---

		// Check if document was closed between timer scheduling and firing
		if !stillOpen {
			log.Printf("[GH][Debounce] Document %s closed before timer fired. Skipping parse.", docURI)
			// Attempt cleanup even if closed, as the timer fired.
			s.debounceTimersMutex.Lock()
			// Check if the timer we are cleaning up is the one actually in the map
			if currentTimerInMap, exists := s.debounceTimers[docURI]; exists && currentTimerInMap == newScheduledTimer {
				delete(s.debounceTimers, docURI)
				log.Printf("[GH][Debounce] Cleaned up timer map entry for closed doc %s", docURI)
			}
			s.debounceTimersMutex.Unlock()
			return // Do not proceed with parsing
		}

		// --- Perform the parse ---
		log.Printf("[GH][Debounce] Calling parseDocument for %s (Hint Tree: %t - Using Full Text)", docURI, treeToParseFrom != nil)
		s.parseDocument(parseCtx, docURI, currentLangID, currentTextBytes, treeToParseFrom)
		log.Printf("[GH][Debounce] parseDocument finished for %s", docURI)
		// --- End Parse ---

		// --- Clean up this specific timer instance from the map ---
		s.debounceTimersMutex.Lock()
		log.Printf("[GH][Debounce] Attempting to delete timer map entry for %s after firing and processing.", docURI)
		// Check if the timer currently stored in the map is the exact instance that just fired.
		// This prevents deleting a *newer* timer if another change came in very quickly.
		if currentTimerInMap, exists := s.debounceTimers[docURI]; exists && currentTimerInMap == newScheduledTimer {
			delete(s.debounceTimers, docURI)
			log.Printf("[GH][Debounce] Successfully deleted fired timer for %s", docURI)
		} else if exists {
			// A different (newer) timer is now in the map, don't delete it. This callback's work is done.
			log.Printf("[GH][Debounce] Skipping delete for %s; a different/newer timer exists in map.", docURI)
		} else {
			// Timer was already removed (e.g., by didClose happening concurrently, or another edge case)
			log.Printf("[GH][Debounce] Timer entry for %s already gone when cleanup ran after processing.", docURI)
		}
		s.debounceTimersMutex.Unlock() // Release timer map lock
		log.Printf("[GH][Debounce] Timer function finished for %s", docURI)

	} // --- End of timer callback function definition ---

	// Create the new timer using the defined callback
	newScheduledTimer = time.AfterFunc(s.debounceDuration, callback)

	// Store the *new* timer instance in the map, overwriting any previous stopped timer reference
	s.debounceTimers[docURI] = newScheduledTimer
	s.debounceTimersMutex.Unlock() // <<< Unlock the timer map mutex >>>

	log.Printf("[GH][didChange] New timer scheduled successfully for %s", docURI)
	// -------------------------- End Debounce Block --------------------------
	log.Println("[GH][didChange] END")
	return nil
}

// --- Grasshopper Feature Handlers ---

// handleInlineCompletion handles 'textDocument/inlineCompletion' requests.
func (s *Server) handleInlineCompletion(ctx context.Context, req lsp.RequestMessage) error {
	if !s.isInitialized() {
		return errors.New("received inlineCompletion before initialized")
	}
	if req.ID == nil {
		return errors.New("inlineCompletion request missing ID")
	}

	var params lsp.InlineCompletionParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		errResp := lsp.ResponseError{Code: lsp.InvalidParams, Message: fmt.Sprintf("Unmarshal params error: %v", err)}
		return s.sendResponse(*req.ID, nil, &errResp)
	}

	docURI := params.TextDocument.URI
	pos := params.Position

	// --- Get Document State ---
	s.stateMutex.RLock()
	docState, ok := s.documents[docURI]
	if !ok {
		s.stateMutex.RUnlock()
		// Send empty list for unknown document
		return s.sendResponse(*req.ID, lsp.InlineCompletionList{}, nil)
	}
	// <<< Make copies under read lock
	docTextBytes := []byte(docState.Text)
	docTree := docState.Tree
	docLangID := docState.LanguageID
	aiClient := s.aiClient
	s.stateMutex.RUnlock() // Release lock before potentially long operations
	// ----------------------

	// Check if AI is available
	if aiClient == nil {
		log.Println("AI client not configured, cannot provide inline suggestion.")
		return s.sendResponse(*req.ID, lsp.InlineCompletionList{}, nil)
	}

	// Check if parsing was successful (AST is available)
	// <<< Add check: If docTree is nil, maybe trigger sync parse like in handleCompletion?
	if docTree == nil || len(docTextBytes) != int(docTree.RootNode().EndByte()) {
		log.Printf("[GH][handleInlineCompletion] Warning: Stale or missing AST detected for %s. Triggering synchronous parse.", docURI)
		s.parseDocument(ctx, docURI, docLangID, docTextBytes, nil) // Force full parse sync

		// Re-fetch the potentially updated state
		s.stateMutex.RLock()
		docState, ok = s.documents[docURI]
		if !ok { // Should not happen if doc opened, but check anyway
			s.stateMutex.RUnlock()
			return s.sendResponse(*req.ID, lsp.InlineCompletionList{}, nil)
		}
		docTree = docState.Tree              // Get potentially updated tree
		docTextBytes = []byte(docState.Text) // Re-read text just in case
		s.stateMutex.RUnlock()

		if docTree == nil { // Check if sync parse failed
			log.Printf("[GH][handleInlineCompletion] Synchronous parse failed or resulted in nil tree.")
			return s.sendResponse(*req.ID, lsp.InlineCompletionList{}, nil)
		}
		log.Printf("[GH][handleInlineCompletion] Synchronous parse completed.")
	}
	// Now we should have a valid tree if parsing worked

	log.Printf("Received inlineCompletion request for %s at L%d:%d", docURI, pos.Line, pos.Character)

	// 1. Convert Position to Offset
	byteOffset, err := position.PositionToOffset(docTextBytes, pos)
	if err != nil {
		log.Printf("Error converting position to offset: %v", err)
		return s.sendResponse(*req.ID, lsp.InlineCompletionList{}, nil)
	}
	log.Printf("Converted position %d:%d to byte offset %d", pos.Line, pos.Character, byteOffset)

	// 2. Find AST Node at Cursor
	rootNode := docTree.RootNode()
	point := calculatePointFromOffset(docTextBytes, byteOffset)             // Use helper
	cursorNode := rootNode.NamedDescendantForPointRange(point, point)       // Use correct function
	log.Printf("Calculated Point: Row=%d, Col=%d", point.Row, point.Column) // Changed Col units label
	if cursorNode != nil {
		log.Printf("Found node at cursor: Type=%s, Range=[%d-%d]", cursorNode.Type(), cursorNode.StartByte(), cursorNode.EndByte())
	} else {
		log.Printf("No named node found directly at cursor point.")
	}

	// 3. Extract Context using Analyzer
	log.Println("Extracting context...")
	extractedContext, err := analyzer.ExtractContext(
		docTextBytes, rootNode, cursorNode, byteOffset, docLangID, string(docURI),
	)
	if err != nil {
		log.Printf("Error extracting context: %v", err)
		return s.sendResponse(*req.ID, lsp.InlineCompletionList{}, nil)
	}
	log.Printf("[GH][handleInlineCompletion] Context extraction successful.") // Adjusted log context

	// 4. Call AI Model
	log.Printf("[GH][handleInlineCompletion] Calling AI client: %s", aiClient.Identify()) // Adjusted log context

	// Prepare context for AI call
	// Use a reasonable timeout for inline suggestions
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	aiSuggestionText, err := aiClient.GetSuggestion(reqCtx, extractedContext)
	if err != nil {
		// Don't treat context cancellation as a server error, just means request was superseded
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			log.Printf("[GH][handleInlineCompletion] AI request timed out or cancelled: %v", err)
			return s.sendResponse(*req.ID, lsp.InlineCompletionList{}, nil) // Send empty list
		}
		log.Printf("[GH][handleInlineCompletion] AI suggestion error: %v", err)
		// Optionally send error response back? Or just empty list?
		// errResp := lsp.ResponseError{Code: lsp.InternalError, Message: fmt.Sprintf("AI Error: %v", err)}
		// return s.sendResponse(*req.ID, nil, &errResp)
		return s.sendResponse(*req.ID, lsp.InlineCompletionList{}, nil) // Send empty list on other AI errors too
	}

	log.Printf("[GH][handleInlineCompletion] GetSuggestion returned.") // Adjusted log context
	if aiSuggestionText == "" {
		log.Println("[GH][handleInlineCompletion] Received empty suggestion from AI.")
		return s.sendResponse(*req.ID, lsp.InlineCompletionList{}, nil)
	}

	// 5. Format Response
	items := []lsp.InlineCompletionItem{{InsertText: aiSuggestionText}}
	result := lsp.InlineCompletionList{Items: items}

	log.Println("Sending inline completion response.")
	return s.sendResponse(*req.ID, result, nil)
}

// handleCompletion handles 'textDocument/completion' (popup menu) requests.
// handleCompletion handles 'textDocument/completion' (popup menu) requests.
func (s *Server) handleCompletion(ctx context.Context, req lsp.RequestMessage) error {
	if !s.isInitialized() {
		return errors.New("received completion before initialized")
	}
	if req.ID == nil {
		return errors.New("completion request missing ID")
	}

	var params lsp.CompletionParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.Printf("[GH][handleCompletion] ERROR: Unmarshal params error: %v", err)
		// Send empty list for bad parameters, common during typing
		return s.sendResponse(*req.ID, lsp.CompletionList{IsIncomplete: false, Items: []lsp.CompletionItem{}}, nil)
	}

	docURI := params.TextDocument.URI
	pos := params.Position

	// --- Get Document State ---
	s.stateMutex.RLock()
	docState, ok := s.documents[docURI]
	if !ok {
		s.stateMutex.RUnlock()
		log.Printf("[GH][handleCompletion] Document not open: %s", docURI)
		return s.sendResponse(*req.ID, lsp.CompletionList{}, nil) // Empty list for unknown document
	}
	// Make copies under read lock
	docTextBytes := []byte(docState.Text)
	docTree := docState.Tree
	docLangID := docState.LanguageID
	aiClient := s.aiClient
	s.stateMutex.RUnlock()
	// ----------------------

	// Check if parsing was successful and AST is up-to-date
	if docTree == nil || len(docTextBytes) != int(docTree.RootNode().EndByte()) {
		log.Printf("[GH][handleCompletion] Warning: Stale or missing AST detected for %s. Triggering synchronous parse.", docURI)
		// Use the parent context for the synchronous parse, it might be cancelled if completion is superseded
		s.parseDocument(ctx, docURI, docLangID, docTextBytes, nil) // Force full parse sync

		// Re-fetch the potentially updated state
		s.stateMutex.RLock()
		docState, ok = s.documents[docURI]
		if !ok {
			s.stateMutex.RUnlock()
			log.Printf("[GH][handleCompletion] Document closed during sync parse: %s", docURI)
			return s.sendResponse(*req.ID, lsp.CompletionList{}, nil)
		}
		docTree = docState.Tree              // Get potentially updated tree
		docTextBytes = []byte(docState.Text) // Re-read text just in case
		s.stateMutex.RUnlock()

		if docTree == nil { // Check if sync parse failed
			log.Printf("[GH][handleCompletion] Synchronous parse failed or resulted in nil tree.")
			return s.sendResponse(*req.ID, lsp.CompletionList{}, nil)
		}
		log.Printf("[GH][handleCompletion] Synchronous parse completed.")
	}

	// Check if AI client is available
	if aiClient == nil {
		log.Println("[GH][handleCompletion] AI client not configured.")
		return s.sendResponse(*req.ID, lsp.CompletionList{}, nil) // No AI, no AI completions
	}
	// We should have a valid docTree now if the above checks passed

	log.Printf("[GH][handleCompletion] Received request for %s at L%d:%d", docURI, pos.Line, pos.Character)
	if params.Context != nil {
		log.Printf("[GH][handleCompletion] TriggerKind: %d, TriggerChar: %s", params.Context.TriggerKind, safeDeref(params.Context.TriggerCharacter))
	}

	// 1. Convert Position
	byteOffset, err := position.PositionToOffset(docTextBytes, pos)
	if err != nil {
		log.Printf("[GH][handleCompletion] Offset conversion error: %v", err)
		return s.sendResponse(*req.ID, lsp.CompletionList{}, nil)
	}
	log.Printf("[GH][handleCompletion] Converted position to byte offset %d", byteOffset)

	// 2. Find Node
	rootNode := docTree.RootNode()
	point := calculatePointFromOffset(docTextBytes, byteOffset)
	cursorNode := rootNode.NamedDescendantForPointRange(point, point)
	log.Printf("[GH][handleCompletion] Calculated Point: Row=%d, Col=%d", point.Row, point.Column)
	if cursorNode != nil {
		log.Printf("[GH][handleCompletion] Node at cursor: Type=%s", cursorNode.Type())
	} else {
		log.Printf("[GH][handleCompletion] No named node at cursor.")
	}

	// 3. Extract Context
	log.Println("[GH][handleCompletion] Extracting context...")
	extractedContext, err := analyzer.ExtractContext(
		docTextBytes, rootNode, cursorNode, byteOffset, docLangID, string(docURI),
	)
	if err != nil {
		log.Printf("[GH][handleCompletion] Context extraction error: %v", err)
		return s.sendResponse(*req.ID, lsp.CompletionList{}, nil)
	}

	// 4. Call AI Model
	log.Printf("[GH][handleCompletion] Calling AI client: %s", aiClient.Identify())
	// Use a timeout for the AI request; adjust as needed
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	aiSuggestionText, err := aiClient.GetSuggestion(reqCtx, extractedContext)
	if err != nil {
		// Don't treat context cancellation as a server error, just means request was superseded
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			log.Printf("[GH][handleCompletion] AI request timed out or cancelled: %v", err)
			return s.sendResponse(*req.ID, lsp.CompletionList{}, nil) // Send empty list
		}
		// Log other AI errors but still return an empty list
		log.Printf("[GH][handleCompletion] AI suggestion error: %v", err)
		return s.sendResponse(*req.ID, lsp.CompletionList{}, nil)
	}

	if aiSuggestionText == "" {
		log.Println("[GH][handleCompletion] Received empty suggestion from AI.")
		// Return empty list, not inline list
		return s.sendResponse(*req.ID, lsp.CompletionList{}, nil)
	}

	// --- 5. Format Response as CompletionItem(s) ---
	var items []lsp.CompletionItem
	if aiSuggestionText != "" {
		// Determine Kind (can be refined later)
		kind := lsp.CompletionItemKindSnippet // Default to snippet as AI might generate complex code

		// Determine InsertTextFormat using defined constants
		insertTextFormat := lsp.InsertTextFormatSnippet // Assume snippet is default
		if !strings.ContainsAny(aiSuggestionText, "${}") && !strings.Contains(aiSuggestionText, "$0") {
			insertTextFormat = lsp.InsertTextFormatPlainText // Use plain text if no snippet syntax detected
		}

		// Create a variable for the detail string to get its address
		detailStr := "(Grasshopper AI)"

		items = append(items, lsp.CompletionItem{
			Label:            aiSuggestionText,  // Use the suggestion as the primary label
			InsertText:       &aiSuggestionText, // Use pointer to the text
			InsertTextFormat: &insertTextFormat, // Assign pointer to the determined format
			Kind:             &kind,             // Use pointer to the kind
			Detail:           &detailStr,        // Use pointer to the detail string variable
			// Documentation can be added later if needed
			// Documentation: &lsp.MarkupContent{Kind: lsp.MarkupKindMarkdown, Value: "Suggestion provided by Grasshopper AI."},
		})
	}
	// --- End of Formatting ---

	// Could potentially add other completion sources here (e.g., keywords, symbols from AST)

	result := lsp.CompletionList{
		IsIncomplete: false, // Set to true if results might change with more typing or if other sources exist
		Items:        items,
	}

	log.Printf("[GH][handleCompletion] Sending %d completion items.", len(result.Items))
	return s.sendResponse(*req.ID, result, nil)
}

// --- Helper Functions ---

// parseDocument performs the actual parsing and updates the server state.
// It now expects `content` to be the full document text.
// `oldTree` is used only as a hint for tree-sitter's incremental parsing optimization.
func (s *Server) parseDocument(ctx context.Context, uri lsp.DocumentURI, langID string, content []byte, oldTree *sitter.Tree) {
	log.Printf("[GH][parseDocument] START for %s", uri) // Log start
	if s.parser == nil {
		log.Printf("[GH][parseDocument] Parser nil, skipping for %s", uri)
		return
	}

	// Log size based on the content actually passed to the parser
	log.Printf("[GH][parseDocument] Parsing document: %s (Lang: %s, IncrementalHint: %t, Size: %d bytes)", uri, langID, oldTree != nil, len(content))
	startTime := time.Now()

	// Tree-sitter can use oldTree as a hint even when parsing full content
	newTree, parseErr := s.parser.Parse(ctx, langID, oldTree, content)
	parseDuration := time.Since(startTime)

	if parseErr != nil {
		log.Printf("[GH][parseDocument] Parser error for %s: %v", uri, parseErr)
		s.logToClient(lsp.TypeError, fmt.Sprintf("Error parsing %s: %v", uri, parseErr))
		newTree = nil // Set tree to nil on fatal parse error
	} else if newTree != nil {
		log.Printf("[GH][parseDocument] Parsed %s in %s", uri, parseDuration)
		if newTree.RootNode().HasError() {
			log.Printf("[GH][parseDocument] Syntax errors found in %s", uri)
			// Maybe debounce sending diagnostics instead of logging here?
			// s.logToClient(lsp.TypeWarning, fmt.Sprintf("Syntax errors found in %s after parse", uri))
		}
	} else {
		// This case shouldn't happen if Parse returns nil err, but log just in case
		log.Printf("[GH][parseDocument] Parsing %s resulted in nil tree without explicit error", uri)
	}

	// Update the tree in the document state
	s.stateMutex.Lock()
	if currentState, ok := s.documents[uri]; ok {
		currentState.Tree = newTree // Update tree (might be nil if parse failed)
		s.documents[uri] = currentState
		log.Printf("[GH][parseDocument] Updated AST state for %s", uri)
	} else {
		log.Printf("[GH][parseDocument] Warning: Document %s closed before parse result stored.", uri)
	}
	s.stateMutex.Unlock()
	log.Printf("[GH][parseDocument] END for %s", uri) // Log end
}

// calculatePointFromOffset helper (needed by completion handlers)
func calculatePointFromOffset(content []byte, byteOffset int) sitter.Point {
	// (Implementation remains the same as before)
	point := sitter.Point{Row: 0, Column: 0}
	currentOffset := 0
	if byteOffset < 0 {
		byteOffset = 0
	}
	if byteOffset > len(content) {
		byteOffset = len(content)
	}
	for i, b := range content { // Use index for precise offset check
		if currentOffset == byteOffset {
			break
		}
		if b == '\n' {
			point.Row++
			point.Column = 0
		} else {
			// Handle UTF-8 potentially? For now, assume column == byte offset on line
			point.Column++
		}
		currentOffset = i + 1 // currentOffset is the offset *after* processing byte `b`
	}
	// If offset is 0, point should be {0, 0} which is default
	// If offset is beyond content length, point will be end of last line
	return point
}

// safeDeref helper (needed by handleCompletion)
func safeDeref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
