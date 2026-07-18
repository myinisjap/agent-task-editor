package providers

// classifyQwenJSON parses one NDJSON line from qwen's stream-json output.
// Qwen reuses the exact same stream-json envelope as the claude CLI, so this
// is currently a thin delegation to classifyStreamJSON — kept as its own
// entry point (rather than having qwen.go call classifyStreamJSON directly)
// so future qwen-specific parsing divergence has a home that doesn't touch
// claude's or the shared format code.
func classifyQwenJSON(line string) streamEvent {
	return classifyStreamJSON(line)
}
