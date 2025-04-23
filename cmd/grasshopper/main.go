package main

import (
	"log"
	"os"

	"github.com/FrancescoCarrabino/grasshopper/internal/server" // <<< ADJUST GITHUB USERNAME
)

func main() {
	// Log to stderr for Neovim's LSP lo
	log.SetOutput(os.Stderr)
	log.Println("Grasshopper LSP server starting...") // Indicate start

	// Create and run the server
	srv := server.NewServer() // NewServer now initializes parser
	if err := srv.Run(os.Stdin, os.Stdout); err != nil {
		log.Printf("FATAL: Server run failed: %v", err)
		os.Exit(1) // Exit with error code if Run fails critically
	}

	log.Println("Grasshopper LSP server stopped.")
}
