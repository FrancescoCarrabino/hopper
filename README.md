[Logo](./static/logo.png)

[![Build Status](https://img.shields.io/github/actions/workflow/status/<FrancescoCarrabino>/hopper/build.yml?branch=main)](https://github.com/<your-username>/<your-repo>/actions) <!-- TODO: Update link after setting up GH Actions -->
[![Latest Release](https://img.shields.io/github/v/release/FrancescoCarrabino/hopper)](https://github.com/<your-username>/<your-repo>/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/FrancescoCarrabino/hopper)](https://goreportcard.com/report/github.com/<your-username>/<your-repo>)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT) <!-- TODO: Update License -->

An AI-powered language server designed to provide intelligent, locally-configurable code completion suggestions, similar to GitHub Copilot, but with support for various AI backends including local models via Ollama and cloud providers.

**Grasshopper brings AI code assistance directly into your editor, giving you control over the models and providers you use.**

---

## ✨ Key Features

*   **Intelligent Code Completion:** Get contextual code suggestions as you type.
*   **Flexible AI Backend Support:** Connect to:
    *   Local models via [Ollama](https://ollama.com/) (e.g., CodeLlama, Qwen Coder, Mistral)
    *   [OpenAI](https://openai.com/) API (GPT-4, GPT-3.5, etc.)
    *   [Azure OpenAI Service](https://azure.microsoft.com/en-us/products/ai-services/openai-service)
    *   [Anthropic Claude](https://www.anthropic.com/) API (Claude 3 models, etc.)
    *   [Google Gemini](https://ai.google.dev/) API
*   **Highly Configurable:** Choose your provider, specific model, API keys/endpoints, and timeouts via a simple configuration file.
*   **Standard LSP Integration:** Works with any editor that supports the Language Server Protocol.
*   **Privacy Focused (with Local Models):** Keep your code private by using locally hosted models via Ollama.

## 🤔 Why Grasshopper?

*   **Control Your AI:** Choose the exact model (local or cloud) that fits your needs and budget.
*   **Privacy:** Leverage the power of local LLMs for code completion without sending your code to third-party services.
*   **Cost-Effective:** Utilize free, powerful open-source models running locally.
*   **Open Source:** Inspect, modify, and contribute to the codebase.

## 🛠️ Supported Languages & Editors

Grasshopper can **parse** the following languages thanks to embedded Tree-sitter grammars:

*   Go (`go`)
*   Python (`python`)
*   JavaScript (`javascript`)
*   Rust (`rust`)
*   Bash (`bash`)
*   YAML (`yaml`)
*   HTML (`html`)

**Completion Quality Note:** While the server can parse these languages, the **prompt engineering** (context extraction in `analyzer.go` and the prompt templates) is currently most refined for **Go**. Completions in other languages might work but may be less accurate or contextually relevant without further tuning of the analyzer rules and prompt templates for those specific languages. Contributions to improve support for other languages are welcome!

**Supported Editors:**

*   Neovim (via `nvim-lspconfig`)
*   Visual Studio Code
*   Any other LSP-compatible editor (configuration may vary)


## 🚀 Demo (Coming Soon!)

*(TODO: Insert a GIF or video showcasing Grasshopper providing completions in an editor)*
<!-- ![Grasshopper Demo](link/to/your/demo.gif) -->
*Watch this space for a demonstration!*

## ⚙️ Installation

**Current Method: Build from Source**

*(Package manager support via Homebrew and Scoop is planned for easier installation!)*

1.  **Prerequisites:**
    *   Go toolchain (version 1.21 or later recommended).
    *   Git.

2.  **Clone the Repository:**
    ```bash
    git clone https://github.com/FrancescoCarrabino/hopper.git
    cd hopper
    ```

3.  **Build the Binary:**
    ```bash
    go build -o grasshopper-lsp ./cmd/grasshopper/main.go
    ```
    *(This builds an executable named `grasshopper-lsp` in the current directory)*

4.  **Move the Binary:** Place the compiled `grasshopper-lsp` executable somewhere accessible. Two common options:

    *   **Option A (Recommended - Add to PATH):** Move it to a directory included in your system's `PATH` environment variable. This allows you to run it simply as `grasshopper-lsp`.
        ```bash
        # Example for Linux/macOS (using ~/.local/bin)
        mkdir -p ~/.local/bin
        mv grasshopper-lsp ~/.local/bin/
        # Make sure ~/.local/bin is in your $PATH!
        # Add 'export PATH="$HOME/.local/bin:$PATH"' to your .bashrc, .zshrc, etc. if needed.
        ```
        *(On Windows, you might move it to a custom tools directory and add that directory to your system or user PATH environment variable.)*

    *   **Option B (Use Full Path):** Move it to any directory (e.g., `~/apps/grasshopper/`). If you do this, you **must** use the full, absolute path when configuring your editor's LSP client later.

5.  **Make Executable (Linux/macOS):**
    ```bash
    chmod +x /path/to/your/grasshopper-lsp # Use the actual path from Step 4
    ```

## 🔧 Configuration

Grasshopper **requires** a configuration file to know which AI provider and model to use.

1.  **Find Your Config Directory:**
    *   **Linux:** `~/.config/grasshopper/`
    *   **macOS:** `~/Library/Application Support/grasshopper/`
    *   **Windows:** `%APPDATA%\grasshopper\` (e.g., `C:\Users\<YourUser>\AppData\Roaming\grasshopper\`)

2.  **Create the Directory:** If the `grasshopper` directory doesn't exist inside your user config directory, create it:
    ```bash
    # Linux/macOS Example:
    mkdir -p ~/.config/grasshopper
    # macOS Example:
    mkdir -p "$HOME/Library/Application Support/grasshopper"
    ```

3.  **Create `config.toml`:** Create a file named `config.toml` inside the `grasshopper` directory.

4.  **Add Configuration:** Populate `config.toml` with your desired settings. **You MUST specify the main `provider`**. Configure the corresponding `[providers.<provider_name>]` section.

    **Sample `config.toml`:**
    ```toml
    # REQUIRED: Specify the main AI provider to use.
    # Options: "ollama", "openai", "azure", "anthropic", "gemini"
    provider = "ollama"

    # Optional: Default request timeout (Go duration format). Defaults to "10s".
    # timeout = "15s"

    # Optional: Default model name IF the chosen provider's specific model isn't set below.
    # model = "some-generic-model" # Usually better to set per-provider

    # --- Provider Specific Settings ---

    [providers.ollama]
    # Host for the Ollama API. Defaults to "http://localhost:11434" if omitted.
    # host = "http://192.168.1.100:11434"
    # REQUIRED: The specific Ollama model name to use (must be pulled in Ollama).
    model = "qwen2.5-coder:3b" # Or "codellama:7b-instruct", "mistral:instruct", etc.

    [providers.openai]
    # API key. Can be omitted if OPENAI_API_KEY environment variable is set.
    # api_key = "sk-..."
    # REQUIRED: Specify the OpenAI model ID. Defaults to "gpt-4o" if omitted.
    model = "gpt-4o" # Or "gpt-3.5-turbo", etc.

    [providers.azure]
    # API key. Can be omitted if AZURE_OPENAI_KEY environment variable is set.
    # api_key = "..."
    # REQUIRED: Your Azure OpenAI resource endpoint URL.
    endpoint = "https://<your-resource-name>.openai.azure.com/"
    # REQUIRED: The deployment name for your model on Azure.
    deployment_id = "<your-deployment-name>"
    # Optional: API Version. Defaults to a recent preview version if omitted.
    # api_version = "2024-02-01"
    # Optional: Model name (usually inferred from deployment, but can override).
    # model = "gpt-4"

    [providers.anthropic]
    # API key. Can be omitted if ANTHROPIC_API_KEY environment variable is set.
    # api_key = "sk-ant-..."
    # REQUIRED: Specify the Anthropic model ID. Defaults to "claude-3-haiku-20240307" if omitted.
    model = "claude-3-opus-20240229" # Or sonnet, haiku
    # Optional: API Version. Defaults to "2023-06-01" if omitted.
    # api_version = "2023-06-01"

    [providers.gemini]
    # API key. Can be omitted if GOOGLE_API_KEY environment variable is set.
    # api_key = "AI..."
    # REQUIRED: Specify the Gemini model ID. Defaults to "gemini-1.5-flash-latest" if omitted.
    model = "gemini-1.5-pro-latest"
    ```

    *   **API Keys:** For cloud providers, it's generally recommended to set API keys using environment variables (`OPENAI_API_KEY`, `AZURE_OPENAI_KEY`, `ANTHROPIC_API_KEY`, `GOOGLE_API_KEY`) instead of putting them directly in the config file. Grasshopper will automatically check these environment variables if the `api_key` field is empty in the TOML file.

## ⚡ Usage

1.  **Configure Your Editor's LSP Client:**
    *   **Neovim:** See [Neovim Setup Guide](docs/NEOVIM_SETUP.md) *(TODO: Create this file)*
    *   **VS Code:** See [VS Code Setup Guide](docs/VSCODE_SETUP.md) *(TODO: Create this file)*
    *   The core task is telling your LSP client how to *run* the `grasshopper-lsp` executable (either by name if in PATH, or by full path). No complex settings are needed in the editor config itself, as the server reads `config.toml`.

2.  **Get Completions:** Open a file in a supported language (see list above). As you type, Grasshopper should automatically provide completion suggestions based on your configured AI backend. You can also usually trigger completions manually (e.g., `Ctrl+Space` in VS Code, check your Neovim completion keybinds).

## 🤝 Contributing

Contributions are welcome! Please feel free to open an issue to report bugs or suggest features, or submit a pull request.

*(TODO: Add contribution guidelines if desired)*

## 📜 License

This project is licensed under the [MIT License](LICENSE). <!-- TODO: Replace MIT with your actual license and add a LICENSE file -->

---

*Made with ❤️ by <Your Name/Org>* <!-- TODO: Update attribution -->
