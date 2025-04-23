package analyzer

import (
	"fmt"
	"log"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// ContextInfo holds structured information extracted from the code
// surrounding a specific point, intended for AI prompting.
type ContextInfo struct {
	LanguageID        string
	Filename          string
	Prefix            string    // Code snippet BEFORE the current line
	Suffix            string    // Code snippet AFTER the current line
	CurrentLinePrefix string    // Part of current line BEFORE cursor
	CurrentLineSuffix string    // Part of current line AFTER cursor
	CursorNode        *NodeInfo // Info about the node directly at the cursor (smallest named node)
	EnclosingNode     *NodeInfo // Info about the nearest relevant enclosing block (function/class/etc.) - Optional Context
	Imports           []string  // List of cleaned imported modules/packages found in the file - Optional Context
}

// NodeInfo provides basic details about a relevant AST node.
type NodeInfo struct {
	Type      string
	Content   string
	StartByte uint32
	EndByte   uint32
}

// --- Language Specific Node Types (Keep as is) ---
var functionNodeTypes = map[string][]string{
	"go":         {"function_declaration", "method_declaration"},
	"python":     {"function_definition"},
	"javascript": {"function_declaration", "function_expression", "arrow_function", "method_definition"},
	"typescript": {"function_declaration", "function_expression", "arrow_function", "method_definition"},
	"rust":       {"function_item", "function_signature_item", "impl_item"},
	"java":       {"method_declaration", "constructor_declaration"},
}

var classNodeTypes = map[string][]string{
	"go":         {"type_spec", "struct_type"},
	"python":     {"class_definition"},
	"javascript": {"class_declaration", "class_expression"},
	"typescript": {"class_declaration", "class_expression", "interface_declaration"},
	"rust":       {"struct_item", "enum_item", "trait_item", "union_item", "impl_item"},
	"java":       {"class_declaration", "interface_declaration", "enum_declaration"},
}

var importNodeTypes = map[string][]string{
	"go":         {"import_declaration", "import_spec"},
	"python":     {"import_statement", "import_from_statement"},
	"javascript": {"import_statement"},
	"typescript": {"import_statement"},
	"rust":       {"use_declaration", "extern_crate_declaration"},
	"java":       {"import_declaration"},
}

// --- Helper Functions ---

// Helper to safely get text content for a node from the full source byte slice.
func getNodeText(node *sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}
	start := node.StartByte()
	end := node.EndByte()
	contentLen := uint32(len(content))
	if start < 0 || end < 0 || start > end || end > contentLen {
		log.Printf("Warning: Invalid bounds for getNodeText. Node type: %s, Range: [%d-%d], Content Len: %d", node.Type(), start, end, contentLen)
		return "[error extracting text bounds]"
	}
	return string(content[start:end])
}

// Helper to find the first ancestor node (including the start node itself)
func findAncestorOfType(startNode *sitter.Node, types []string) *sitter.Node {
	if startNode == nil || len(types) == 0 {
		return nil
	}
	typeSet := make(map[string]struct{}, len(types))
	for _, t := range types {
		typeSet[t] = struct{}{}
	}

	currentNode := startNode
	for currentNode != nil {
		if _, ok := typeSet[currentNode.Type()]; ok {
			return currentNode
		}
		currentNode = currentNode.Parent()
	}
	return nil
}

// Helper function to calculate sitter.Point from byte offset.
func calculatePointFromOffset(content []byte, byteOffset int) sitter.Point {
	point := sitter.Point{Row: 0, Column: 0}
	currentOffset := 0
	if byteOffset < 0 {
		byteOffset = 0
	}
	if byteOffset > len(content) {
		byteOffset = len(content)
	}
	for i, b := range content {
		if currentOffset == byteOffset {
			break
		}
		if b == '\n' {
			point.Row++
			point.Column = 0
		} else {
			point.Column++
		}
		currentOffset = i + 1
	}
	return point
}

// Helper for safe logging of NodeInfo type
func safeNodeType(nodeInfo *NodeInfo) string {
	if nodeInfo == nil {
		return "nil"
	}
	return nodeInfo.Type
}

// Helper to safely get the Type string from a sitter.Node pointer
func safeGetNodeType(node *sitter.Node) string {
	if node == nil {
		return "nil"
	}
	return node.Type()
}

// --- Main Extraction Logic ---

// ExtractContext analyzes the code around the cursor and returns structured context.
// Calculates Prefix/Suffix relative to the *current line* and also calculates
// the parts of the current line before/after the cursor.
func ExtractContext(
	content []byte,
	rootNode *sitter.Node,
	passedCursorNode *sitter.Node, // Node from handler (might be nil or ERROR)
	cursorByteOffset int,
	languageID string,
	filename string,
) (*ContextInfo, error) {
	if rootNode == nil {
		return nil, fmt.Errorf("cannot extract context without root node")
	}
	contentLen := len(content) // Store length for bounds checks

	// Basic validation of cursor offset
	if cursorByteOffset < 0 {
		cursorByteOffset = 0
	} else if cursorByteOffset > contentLen {
		cursorByteOffset = contentLen
	}

	ctxInfo := &ContextInfo{
		LanguageID: languageID,
		Filename:   filename,
		Imports:    make([]string, 0),
		// Initialize string fields to empty to avoid nil pointers later
		Prefix:            "",
		Suffix:            "",
		CurrentLinePrefix: "",
		CurrentLineSuffix: "",
	}

	// --- 1. Determine Node at Cursor & Parent (for logging/debugging) ---
	cursorNode := passedCursorNode
	var cursorParentNode *sitter.Node
	point := calculatePointFromOffset(content, cursorByteOffset) // Calculate once

	if cursorNode != nil {
		ctxInfo.CursorNode = &NodeInfo{ // Store info about the node LSP found
			Type:      cursorNode.Type(),
			Content:   getNodeText(cursorNode, content),
			StartByte: cursorNode.StartByte(),
			EndByte:   cursorNode.EndByte(),
		}
		cursorParentNode = cursorNode.Parent()
	} else {
		// If handler didn't find a node, try again here
		cursorNode = rootNode.NamedDescendantForPointRange(point, point)
		if cursorNode != nil {
			cursorParentNode = cursorNode.Parent()
			// Optionally store this found node in ctxInfo.CursorNode as well?
			ctxInfo.CursorNode = &NodeInfo{
				Type:      cursorNode.Type(),
				Content:   getNodeText(cursorNode, content),
				StartByte: cursorNode.StartByte(),
				EndByte:   cursorNode.EndByte(),
			}
			log.Printf("Analyzer found named node at cursor point: Type=%s", cursorNode.Type())
		} else {
			log.Printf("Analyzer: No named node found at cursor point %v", point)
		}
	}

	// --- 2. Calculate Current Line Boundaries ---
	lineStartByte := cursorByteOffset
	// Go backwards to find the start of the line or file beginning
	for lineStartByte > 0 && content[lineStartByte-1] != '\n' {
		lineStartByte--
	}

	lineEndByte := cursorByteOffset
	// Go forwards to find the newline character or end of file
	for lineEndByte < contentLen && content[lineEndByte] != '\n' {
		lineEndByte++
	}
	// lineEndByte now points AT the newline or is equal to contentLen

	// --- 3. Calculate Current Line Prefix/Suffix ---
	currentLinePrefixEndByte := cursorByteOffset
	// Safety checks for bounds
	if currentLinePrefixEndByte < lineStartByte {
		currentLinePrefixEndByte = lineStartByte
	}
	if currentLinePrefixEndByte > lineEndByte {
		currentLinePrefixEndByte = lineEndByte
	}

	if currentLinePrefixEndByte > lineStartByte { // Use > to avoid empty slice if cursor is at start
		ctxInfo.CurrentLinePrefix = string(content[lineStartByte:currentLinePrefixEndByte])
	}

	currentLineSuffixStartByte := cursorByteOffset
	// Safety checks for bounds
	if currentLineSuffixStartByte < lineStartByte {
		currentLineSuffixStartByte = lineStartByte
	}
	if currentLineSuffixStartByte > lineEndByte {
		currentLineSuffixStartByte = lineEndByte
	}

	if lineEndByte > currentLineSuffixStartByte { // Use > to avoid empty slice if cursor is at end of line (before \n)
		ctxInfo.CurrentLineSuffix = string(content[currentLineSuffixStartByte:lineEndByte])
	}

	// --- 4. Calculate Broader Prefix (Code BEFORE Current Line) ---
	const prefixContextBytes = 2048 // How many bytes *before* the current line to include
	prefixStartByte := lineStartByte - prefixContextBytes
	if prefixStartByte < 0 {
		prefixStartByte = 0
	}
	// End of the prefix is the start of the current line
	prefixEndByte := lineStartByte
	if prefixEndByte > prefixStartByte { // Use > to avoid empty slice
		ctxInfo.Prefix = string(content[prefixStartByte:prefixEndByte])
	}

	// --- 5. Calculate Broader Suffix (Code AFTER Current Line) ---
	const suffixContextBytes = 256 // How many bytes *after* the current line to include
	suffixStartByte := lineEndByte
	// Skip the newline character itself if it exists and we're not at EOF
	if suffixStartByte < contentLen && content[suffixStartByte] == '\n' {
		suffixStartByte++
	}

	suffixEndByte := suffixStartByte + suffixContextBytes
	if suffixEndByte > contentLen {
		suffixEndByte = contentLen
	}

	if suffixEndByte > suffixStartByte { // Use > to avoid empty slice
		ctxInfo.Suffix = string(content[suffixStartByte:suffixEndByte])
	}

	// --- 6. Extract Imports (Optional Context) ---
	importTypes, languageHasImports := importNodeTypes[languageID]
	if languageHasImports && len(importTypes) > 0 {
		importTypeSet := make(map[string]struct{}, len(importTypes))
		for _, t := range importTypes {
			importTypeSet[t] = struct{}{}
		}

		treeCursor := sitter.NewTreeCursor(rootNode) // Use different variable name
		defer treeCursor.Close()
		visitedImports := make(map[string]bool)
		hasNext := true
		for hasNext {
			node := treeCursor.CurrentNode()
			if _, isImportType := importTypeSet[node.Type()]; isImportType {
				importText := getNodeText(node, content)
				cleanedImport := cleanImportText(importText, languageID) // Assuming cleanImportText exists
				if cleanedImport != "" && !visitedImports[cleanedImport] {
					ctxInfo.Imports = append(ctxInfo.Imports, cleanedImport)
					visitedImports[cleanedImport] = true
				}
			}
			// Traversal logic
			if treeCursor.GoToFirstChild() {
				continue
			}
			if treeCursor.GoToNextSibling() {
				continue
			}
			for treeCursor.GoToParent() {
				if treeCursor.GoToNextSibling() {
					goto next_import_iteration
				}
			}
			hasNext = false
		next_import_iteration:
		}
	} // End import extraction

	// --- 7. Find Enclosing Function/Class Block (Optional Context) ---
	// Use the node identified at/near the cursor for the search start
	searchStartNodeForEnclosing := cursorNode
	if searchStartNodeForEnclosing == nil {
		searchStartNodeForEnclosing = rootNode.NamedDescendantForPointRange(point, point)
	}
	enclosingTypes := append(functionNodeTypes[languageID], classNodeTypes[languageID]...)
	enclosingBlockNode := findAncestorOfType(searchStartNodeForEnclosing, enclosingTypes)
	if enclosingBlockNode != nil {
		ctxInfo.EnclosingNode = &NodeInfo{
			Type:      enclosingBlockNode.Type(),
			Content:   getNodeText(enclosingBlockNode, content), // Consider truncating later
			StartByte: enclosingBlockNode.StartByte(),
			EndByte:   enclosingBlockNode.EndByte(),
		}
	}
	// --- End Enclosing Block ---

	// --- Final Logging ---
	log.Printf("Analyzer Context Extracted: CursorNode Type:%s, ParentNode Type:%s, EnclosingNode Type:%s, PrefixLen=%d, SuffixLen=%d, CurrentLinePrefixLen=%d, CurrentLineSuffixLen=%d, Imports=%d",
		safeGetNodeType(cursorNode),         // Log type of node at cursor
		safeGetNodeType(cursorParentNode),   // Log type of parent
		safeNodeType(ctxInfo.EnclosingNode), // Log type from stored NodeInfo
		len(ctxInfo.Prefix),                 // Broader prefix length
		len(ctxInfo.Suffix),                 // Broader suffix length
		len(ctxInfo.CurrentLinePrefix),      // Current line prefix length
		len(ctxInfo.CurrentLineSuffix),      // Current line suffix length
		len(ctxInfo.Imports))                // Number of imports

	return ctxInfo, nil
}

// cleanImportText performs basic cleaning on extracted import node text.
// Needs language-specific improvements for accuracy.
func cleanImportText(importText string, languageID string) string {
	cleaned := strings.TrimSpace(importText)
	switch languageID {
	case "go":
		cleaned = strings.Trim(cleaned, `"`)
		cleaned = strings.TrimPrefix(cleaned, "import (")
		cleaned = strings.TrimPrefix(cleaned, "import")
		cleaned = strings.TrimSuffix(cleaned, ")")
		cleaned = strings.TrimSpace(cleaned)
		// Avoid adding empty strings if cleaning removed everything
		if len(cleaned) == 0 || cleaned == "(" || cleaned == ")" {
			return ""
		}
	case "python":
		cleaned = strings.ReplaceAll(cleaned, "\n", " ")
		cleaned = strings.Join(strings.Fields(cleaned), " ")
	case "javascript", "typescript":
		cleaned = strings.ReplaceAll(cleaned, "\n", " ")
		cleaned = strings.Join(strings.Fields(cleaned), " ")
	case "rust":
		cleaned = strings.TrimPrefix(cleaned, "use ")
		cleaned = strings.TrimPrefix(cleaned, "extern crate ")
		cleaned = strings.TrimSuffix(cleaned, ";")
		cleaned = strings.TrimSpace(cleaned)
	case "java":
		cleaned = strings.TrimPrefix(cleaned, "import ")
		cleaned = strings.TrimSuffix(cleaned, ";")
		cleaned = strings.TrimPrefix(cleaned, "static ")
		cleaned = strings.TrimSpace(cleaned)
	}
	// Final check for empty string after cleaning
	if len(strings.Fields(cleaned)) == 0 { // Check if it became effectively empty
		return ""
	}
	return cleaned
}
