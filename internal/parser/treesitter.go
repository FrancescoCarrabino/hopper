package parser

import (
	"context"
	"fmt"
	"log"

	// "os" // Keep os if using external grammars
	// "path/filepath"
	// "runtime"
	"sync"

	// Use the primary import path
	sitter "github.com/smacker/go-tree-sitter"

	// Import the specific language packages
	"github.com/smacker/go-tree-sitter/bash"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/html"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/yaml"
	// Add others as needed
)

// Manager struct remains the same
type Manager struct {
	parser  *sitter.Parser
	langMap map[string]*sitter.Language
	mu      sync.RWMutex
}

// NewManager remains the same (using embedded grammars)
func NewManager() (*Manager, error) {
	parser := sitter.NewParser()
	m := &Manager{
		parser:  parser,
		langMap: make(map[string]*sitter.Language),
	}
	log.Println("Initializing parser manager with embedded grammars...")

	m.langMap["go"] = golang.GetLanguage()
	m.langMap["python"] = python.GetLanguage()
	m.langMap["javascript"] = javascript.GetLanguage()
	m.langMap["rust"] = rust.GetLanguage()
	m.langMap["bash"] = bash.GetLanguage()
	m.langMap["yaml"] = yaml.GetLanguage()
	m.langMap["html"] = html.GetLanguage()
	// Add others...

	log.Printf("Loaded %d embedded grammars.", len(m.langMap))
	// Check for nil languages...
	for langID, lang := range m.langMap {
		if lang == nil {
			log.Printf("Warning: Embedded grammar for language ID '%s' loaded as nil.", langID)
		}
	}
	return m, nil
}

func (m *Manager) Parse(ctx context.Context, langID string, oldTree *sitter.Tree, content []byte) (*sitter.Tree, error) {
	m.mu.RLock()
	lang, ok := m.langMap[langID]
	m.mu.RUnlock()
	if !ok {
		return nil, nil
	} // Language not supported
	if lang == nil {
		return nil, fmt.Errorf("internal error: language object for '%s' is nil", langID)
	}

	m.parser.SetLanguage(lang)

	// Pass the oldTree (potentially nil or edited) to ParseCtx.
	// Tree-sitter uses this for incremental parsing if possible.
	newTree, err := m.parser.ParseCtx(ctx, oldTree, content)
	if err != nil {
		return nil, fmt.Errorf("parsing failed for lang %s: %w", langID, err)
	}

	return newTree, nil
}

// Close remains the same
func (m *Manager) Close() {
	if m.parser != nil {
		m.parser.Close()
	}
}
