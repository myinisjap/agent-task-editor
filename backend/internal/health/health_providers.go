package health

// claudeCheck verifies the claude CLI is installed and appears authenticated.
// Authentication is detected heuristically (credential file or env var) rather
// than by invoking the CLI, so a green result means "credentials found", not a
// live token validation.
func claudeCheck(d Deps) Check {
	c := Check{ID: "claude_cli", Name: "Claude CLI"}
	if _, err := d.LookPath("claude"); err != nil {
		c.Status = StatusError
		c.Detail = "claude binary not found on PATH"
		c.Hint = "Install the Claude CLI (npm i -g @anthropic-ai/claude-code) so the claude provider can run."
		return c
	}
	if claudeAuthenticated(d) {
		c.Status = StatusOK
		c.Detail = "claude CLI installed and credentials found"
		return c
	}
	c.Status = StatusWarn
	c.Detail = "claude CLI installed but no credentials detected"
	c.Hint = "Run `claude` once to log in, or mount ~/.claude/.credentials.json / set ANTHROPIC_API_KEY. Runs may fail with \"Not logged in\"."
	return c
}

// claudeAuthenticated reports whether Claude credentials appear to be present:
// an ANTHROPIC_API_KEY env var, or a ~/.claude/.credentials.json file.
func claudeAuthenticated(d Deps) bool {
	if d.Getenv("ANTHROPIC_API_KEY") != "" {
		return true
	}
	if home, err := d.HomeDir(); err == nil {
		if d.FileExists(home + "/.claude/.credentials.json") {
			return true
		}
	}
	return false
}

// anthropicCheck verifies the direct Anthropic Messages API provider has a key.
func anthropicCheck(in Input, _ Deps) Check {
	c := Check{ID: "anthropic_api", Name: "Anthropic API key"}
	if in.LLMAPIKey == "" {
		c.Status = StatusError
		c.Detail = "LLM_API_KEY is not set"
		c.Hint = "Set LLM_API_KEY to an Anthropic API key; the anthropic provider bills per-token."
		return c
	}
	c.Status = StatusOK
	c.Detail = "LLM_API_KEY is set"
	return c
}

// llmCheck verifies the OpenAI-compatible provider has a key and base URL.
func llmCheck(in Input) Check {
	c := Check{ID: "llm_api", Name: "LLM API (OpenAI-compatible)"}
	if in.LLMAPIKey == "" {
		c.Status = StatusError
		c.Detail = "LLM_API_KEY is not set"
		c.Hint = "Set LLM_API_KEY for the llm provider's OpenAI-compatible endpoint."
		return c
	}
	if in.LLMBaseURL == "" {
		c.Status = StatusWarn
		c.Detail = "LLM_API_KEY set, but LLM_BASE_URL is empty"
		c.Hint = "Set LLM_BASE_URL to your provider's endpoint (defaults to https://api.openai.com/v1)."
		return c
	}
	c.Status = StatusOK
	c.Detail = "LLM_API_KEY set; base URL " + in.LLMBaseURL
	return c
}

// geminiCheck verifies the gemini CLI is installed and appears authenticated.
// Authentication is detected heuristically (env var or the OAuth cache file
// the CLI itself writes on `gemini` login) rather than by invoking the CLI.
func geminiCheck(d Deps) Check {
	c := Check{ID: "gemini_cli", Name: "Gemini CLI"}
	if _, err := d.LookPath("gemini"); err != nil {
		c.Status = StatusError
		c.Detail = "gemini binary not found on PATH"
		c.Hint = "Install the Gemini CLI (npm i -g @google/gemini-cli) so the gemini_cli provider can run."
		return c
	}
	if geminiAuthenticated(d) {
		c.Status = StatusOK
		c.Detail = "gemini CLI installed and credentials found"
		return c
	}
	c.Status = StatusWarn
	c.Detail = "gemini CLI installed but no credentials detected"
	c.Hint = "Run `gemini` once to log in with a Google account, or set GEMINI_API_KEY / GOOGLE_API_KEY. Runs may fail with an auth error."
	return c
}

// geminiAuthenticated reports whether Gemini CLI credentials appear to be
// present: a GEMINI_API_KEY/GOOGLE_API_KEY env var, or the OAuth credential
// cache the CLI writes to ~/.gemini/oauth_creds.json on `gemini` login.
func geminiAuthenticated(d Deps) bool {
	if d.Getenv("GEMINI_API_KEY") != "" || d.Getenv("GOOGLE_API_KEY") != "" {
		return true
	}
	if home, err := d.HomeDir(); err == nil {
		if d.FileExists(home + "/.gemini/oauth_creds.json") {
			return true
		}
	}
	return false
}

// codexCheck verifies the codex CLI is installed and appears authenticated.
// Authentication is detected heuristically (env var or the auth cache file
// the CLI itself writes on `codex login`) rather than by invoking the CLI.
func codexCheck(d Deps) Check {
	c := Check{ID: "codex_cli", Name: "Codex CLI"}
	if _, err := d.LookPath("codex"); err != nil {
		c.Status = StatusError
		c.Detail = "codex binary not found on PATH"
		c.Hint = "Install the Codex CLI (npm i -g @openai/codex) so the codex_cli provider can run."
		return c
	}
	if codexAuthenticated(d) {
		c.Status = StatusOK
		c.Detail = "codex CLI installed and credentials found"
		return c
	}
	c.Status = StatusWarn
	c.Detail = "codex CLI installed but no credentials detected"
	c.Hint = "Run `codex login` to sign in with ChatGPT, or set OPENAI_API_KEY. Runs may fail with a 401 auth error."
	return c
}

// codexAuthenticated reports whether Codex CLI credentials appear to be
// present: an OPENAI_API_KEY env var, or the auth cache file the CLI writes
// to ~/.codex/auth.json on `codex login`.
func codexAuthenticated(d Deps) bool {
	if d.Getenv("OPENAI_API_KEY") != "" {
		return true
	}
	if home, err := d.HomeDir(); err == nil {
		if d.FileExists(home + "/.codex/auth.json") {
			return true
		}
	}
	return false
}

// binaryCheck is the shared "is this CLI on PATH" check for qwen/opencode.
func binaryCheck(d Deps, bin, name, usedBy, hint string) Check {
	c := Check{ID: bin + "_cli", Name: name}
	if _, err := d.LookPath(bin); err != nil {
		c.Status = StatusError
		c.Detail = bin + " binary not found on PATH (required by " + usedBy + ")"
		c.Hint = hint
		return c
	}
	c.Status = StatusOK
	c.Detail = bin + " binary found on PATH"
	return c
}

// mcpCheck verifies the MCP sidecar binary is configured and present.
func mcpCheck(in Input, d Deps) Check {
	c := Check{ID: "mcp_sidecar", Name: "MCP sidecar"}
	if in.MCPBinary == "" {
		c.Status = StatusWarn
		c.Detail = "MCP_SERVER_PATH is not set"
		c.Hint = "Set MCP_SERVER_PATH to the mcp-server binary to enable signal_complete/request_human for claude/qwen agents."
		return c
	}
	if !d.FileExists(in.MCPBinary) {
		c.Status = StatusError
		c.Detail = "MCP_SERVER_PATH is set but the file does not exist: " + in.MCPBinary
		c.Hint = "Point MCP_SERVER_PATH at the built mcp-server binary."
		return c
	}
	c.Status = StatusOK
	c.Detail = "MCP sidecar configured: " + in.MCPBinary
	return c
}
