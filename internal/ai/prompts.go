package ai

import (
	"embed" // Import the embed package
)

//go:embed prompts/*/*.tmpl
var promptFS embed.FS
