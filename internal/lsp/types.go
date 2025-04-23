package lsp

import "encoding/json"

// Using string for URI for simplicity, could use net/url later if needed
type DocumentURI string

// RequestMessage represents a JSON-RPC request or notification.
type RequestMessage struct {
	RPCVersion string          `json:"jsonrpc"`
	ID         *int            `json:"id,omitempty"` // Pointer to distinguish request (ID set) from notification (ID nil)
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params,omitempty"` // Delay parsing params until method is known
}

// ResponseMessage represents a JSON-RPC response.
type ResponseMessage struct {
	RPCVersion string          `json:"jsonrpc"`
	ID         *int            `json:"id"` // Must be present in responses
	Result     json.RawMessage `json:"result,omitempty"`
	Error      *ResponseError  `json:"error,omitempty"`
}

// ResponseError represents a JSON-RPC error object.
type ResponseError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Standard JSON-RPC error codes
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

// InsertTextFormat defines whether the insert text is plain text or a snippet.
type InsertTextFormat int

const (
	// InsertTextFormatPlainText The insert text is treated as plain text.
	InsertTextFormatPlainText InsertTextFormat = 1
	// InsertTextFormatSnippet The insert text is treated as a snippet. A snippet can define tab stops and placeholders variables using `$1`, `$2` and `${3:placeholder}`. `$0` defines the final tab stop. Escaped `$` characters ($$) are ignored.
	InsertTextFormatSnippet InsertTextFormat = 2
)

// CompletionItem represents a single completion suggestion.
type CompletionItem struct {
	Label            string              `json:"label"`                      // The label of this completion item. By default also the text that is inserted when selecting this completion.
	Kind             *CompletionItemKind `json:"kind,omitempty"`             // The kind of this completion item. Based on the kind an icon is chosen by the editor.
	Detail           *string             `json:"detail,omitempty"`           // A human-readable string with additional information about this item, like type or symbol information.
	Documentation    *string             `json:"documentation,omitempty"`    // A human-readable string that represents a doc-comment. (Can also be MarkupContent)
	InsertText       *string             `json:"insertText,omitempty"`       // A string that should be inserted into the document when selecting this completion. When omitted the label is used.
	InsertTextFormat *InsertTextFormat   `json:"insertTextFormat,omitempty"` // <<< ADDED: The format of the insert text e.g. plain text or snippet.
	// Add more fields as needed: TextEdit, AdditionalTextEdits, CommitCharacters, Command, etc.
}

// InitializeParams corresponds to the 'initialize' request parameters.
type InitializeParams struct {
	ProcessID             *int                   `json:"processId,omitempty"`
	RootURI               *DocumentURI           `json:"rootUri,omitempty"`
	ClientInfo            *ClientInfo            `json:"clientInfo,omitempty"`
	InitializationOptions map[string]interface{} `json:"initializationOptions,omitempty"`
	Capabilities          ClientCapabilities     `json:"capabilities"`
	// Add other fields as needed: workspaceFolders, trace, locale...
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ClientCapabilities - Define only what we might check initially
type ClientCapabilities struct {
	Workspace    *WorkspaceClientCapabilities    `json:"workspace,omitempty"`
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
	// Add other capability sections if needed
}

type WorkspaceClientCapabilities struct {
	Configuration          *bool `json:"configuration,omitempty"`
	DidChangeConfiguration *struct {
		DynamicRegistration *bool `json:"dynamicRegistration,omitempty"`
	} `json:"didChangeConfiguration,omitempty"`
	// Add others... applyEdit, workspaceEdit, workspaceFolders...
}

type TextDocumentClientCapabilities struct {
	Synchronization  *TextDocumentSyncClientCapabilities `json:"synchronization,omitempty"`
	Completion       *CompletionClientCapabilities       `json:"completion,omitempty"`
	InlineCompletion *InlineCompletionClientCapabilities `json:"inlineCompletion,omitempty"`
	// Add others... hover, definition, references...
}

type TextDocumentSyncClientCapabilities struct {
	DidSave *bool `json:"didSave,omitempty"` // Example
}
type CompletionClientCapabilities struct {
	CompletionItem *struct {
		SnippetSupport *bool `json:"snippetSupport,omitempty"`
	} `json:"completionItem,omitempty"` // Example
}
type InlineCompletionClientCapabilities struct {
	DynamicRegistration *bool `json:"dynamicRegistration,omitempty"` // Example
}

// InitializeResult corresponds to the 'initialize' response result.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ServerCapabilities defines the capabilities of our server.
type ServerCapabilities struct {
	TextDocumentSync         *TextDocumentSyncOptions `json:"textDocumentSync,omitempty"`
	CompletionProvider       *CompletionOptions       `json:"completionProvider,omitempty"`
	InlineCompletionProvider *InlineCompletionOptions `json:"inlineCompletionProvider,omitempty"` // Can be bool or options
	// Add other capabilities like hoverProvider, definitionProvider etc. as features are added
}

// TextDocumentSyncKind defines how documents are synced.
type TextDocumentSyncKind int

const (
	SyncNone        TextDocumentSyncKind = 0
	SyncFull        TextDocumentSyncKind = 1 // Send full content on change
	SyncIncremental TextDocumentSyncKind = 2 // Send incremental changes
)

type TextDocumentSyncOptions struct {
	OpenClose         *bool                 `json:"openClose,omitempty"` // Send open/close notifications
	Change            *TextDocumentSyncKind `json:"change,omitempty"`    // Sync kind
	WillSave          *bool                 `json:"willSave,omitempty"`
	WillSaveWaitUntil *bool                 `json:"willSaveWaitUntil,omitempty"`
	Save              *SaveOptions          `json:"save,omitempty"`
}

type SaveOptions struct {
	IncludeText *bool `json:"includeText,omitempty"`
}

type CompletionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
	ResolveProvider   *bool    `json:"resolveProvider,omitempty"` // Support completionItem/resolve?
}

type InlineCompletionOptions struct { // Can just be 'true' or an object - use interface{} or a struct
	// Currently no standard options defined, but could be used for custom things if needed
}

// DidOpenTextDocumentParams corresponds to 'textDocument/didOpen' notification parameters.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type TextDocumentItem struct {
	URI        DocumentURI `json:"uri"`
	LanguageID string      `json:"languageId"`
	Version    int         `json:"version"`
	Text       string      `json:"text"`
}

// DidCloseTextDocumentParams corresponds to 'textDocument/didClose' notification parameters.
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type TextDocumentIdentifier struct {
	URI DocumentURI `json:"uri"`
}

// InlineCompletionParams corresponds to 'textDocument/inlineCompletion' request parameters.
type InlineCompletionParams struct {
	TextDocument TextDocumentIdentifier  `json:"textDocument"`
	Position     Position                `json:"position"`
	Context      InlineCompletionContext `json:"context"`
}

type InlineCompletionContext struct {
	TriggerKind            InlineCompletionTriggerKind `json:"triggerKind"`
	SelectedCompletionInfo *SelectedCompletionInfo     `json:"selectedCompletionInfo,omitempty"`
}

type InlineCompletionTriggerKind int

const (
	TriggerInvoke    InlineCompletionTriggerKind = 0 // Explicitly invoked (e.g., via command)
	TriggerAutomatic InlineCompletionTriggerKind = 1 // Triggered automatically during typing
)

type SelectedCompletionInfo struct {
	Range Range  `json:"range"`
	Text  string `json:"text"`
}

// InlineCompletionList corresponds to the 'textDocument/inlineCompletion' response result.
type InlineCompletionList struct {
	Items []InlineCompletionItem `json:"items"`
}

type InlineCompletionItem struct {
	InsertText string   // Can be string or SnippetString, start simple
	FilterText *string  `json:"filterText,omitempty"`
	Range      *Range   `json:"range,omitempty"`   // Range to replace, defaults to word/cursor area if omitted
	Command    *Command `json:"command,omitempty"` // Command executed after insertion
	// Add other fields like trackingId if needed later
}

// Command is used in completion items, code actions etc.
type Command struct {
	Title     string        `json:"title"`
	Command   string        `json:"command"` // The command identifier
	Arguments []interface{} `json:"arguments,omitempty"`
}

// LogMessageParams corresponds to 'window/logMessage' notification parameters.
type LogMessageParams struct {
	Type    MessageType `json:"type"`
	Message string      `json:"message"`
}
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type VersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version int         `json:"version"` // The version number AFTER the change
}

// TextDocumentContentChangeEvent represents changes in a document.
// It can represent the full text of the document or an incremental change.
type TextDocumentContentChangeEvent struct {
	// The range of the document that changed.
	// This field is REQUIRED for incremental changes.
	// It is OPTIONAL and should be omitted for SyncKind.Full.
	Range *Range `json:"range,omitempty"`

	// The length of the range that got replaced.
	// REQUIRED for incremental changes. OPTIONAL for SyncKind.Full.
	RangeLength *uint32 `json:"rangeLength,omitempty"` // Use uint32 as length cannot be negative

	// The new text for the provided range (incremental) or the whole document (full).
	Text string `json:"text"`
}

// Range represents a range in a text document defined by start and end positions.
// Ensure this struct is also defined (it was in earlier versions of types.go).
type Range struct {
	Start Position `json:"start"` // The range's start position (inclusive).
	End   Position `json:"end"`   // The range's end position (exclusive).
}

// Position represents a position in a text document (0-based line and character).
// Ensure this struct is also defined.
type Position struct {
	Line      int `json:"line"`      // zero-based
	Character int `json:"character"` // zero-based UTF-16 code unit offset
}

// CompletionParams parameters for textDocument/completion
type CompletionParams struct {
	TextDocumentPositionParams                    // Embeds TextDocument and Position
	Context                    *CompletionContext `json:"context,omitempty"`
}

// TextDocumentPositionParams is a parameter literal used in requests to pass a text document
// identifier and a position inside that document.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// CompletionContext contains additional information about the context in which
// a completion request is triggered.
type CompletionContext struct {
	TriggerKind      CompletionTriggerKind `json:"triggerKind"`
	TriggerCharacter *string               `json:"triggerCharacter,omitempty"` // The trigger character if triggerKind is TriggerCharacter
}

// CompletionTriggerKind defines how a completion was triggered.
type CompletionTriggerKind int

const (
	// CompletionTriggerKindInvoked completion was triggered by typing an identifier etc. (applied semantic)
	CompletionTriggerKindInvoked CompletionTriggerKind = 1
	// CompletionTriggerKindTriggerCharacter completion was triggered by a trigger character (applied semantic)
	CompletionTriggerKindTriggerCharacter CompletionTriggerKind = 2
	// CompletionTriggerKindTriggerForIncompleteCompletions completion was re-triggered as the current completion list is incomplete.
	CompletionTriggerKindTriggerForIncompleteCompletions CompletionTriggerKind = 3
)

// CompletionList represents a list of completion items to be presented
// in the editor.
type CompletionList struct {
	// This list it not complete. Further typing should result in recomputing
	// this list.
	IsIncomplete bool `json:"isIncomplete"`
	// The completion items.
	Items []CompletionItem `json:"items"`
}

// CompletionItemKind defines the kind of a completion item.
type CompletionItemKind int

// Constants for CompletionItemKind (add more as needed)
const (
	CompletionItemKindText          CompletionItemKind = 1
	CompletionItemKindMethod        CompletionItemKind = 2
	CompletionItemKindFunction      CompletionItemKind = 3
	CompletionItemKindConstructor   CompletionItemKind = 4
	CompletionItemKindField         CompletionItemKind = 5
	CompletionItemKindVariable      CompletionItemKind = 6
	CompletionItemKindClass         CompletionItemKind = 7
	CompletionItemKindInterface     CompletionItemKind = 8
	CompletionItemKindModule        CompletionItemKind = 9
	CompletionItemKindProperty      CompletionItemKind = 10
	CompletionItemKindUnit          CompletionItemKind = 11
	CompletionItemKindValue         CompletionItemKind = 12
	CompletionItemKindEnum          CompletionItemKind = 13
	CompletionItemKindKeyword       CompletionItemKind = 14
	CompletionItemKindSnippet       CompletionItemKind = 15
	CompletionItemKindColor         CompletionItemKind = 16
	CompletionItemKindFile          CompletionItemKind = 17
	CompletionItemKindReference     CompletionItemKind = 18
	CompletionItemKindFolder        CompletionItemKind = 19
	CompletionItemKindEnumMember    CompletionItemKind = 20
	CompletionItemKindConstant      CompletionItemKind = 21
	CompletionItemKindStruct        CompletionItemKind = 22
	CompletionItemKindEvent         CompletionItemKind = 23
	CompletionItemKindOperator      CompletionItemKind = 24
	CompletionItemKindTypeParameter CompletionItemKind = 25
)

type MessageType int

const (
	TypeError   MessageType = 1
	TypeWarning MessageType = 2
	TypeInfo    MessageType = 3
	TypeLog     MessageType = 4
)
