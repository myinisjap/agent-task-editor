package providers

import (
	"bufio"
	"io"
)

// maxScanLineBytes is the maximum size of a single line this package's CLI
// providers will buffer while scanning a subprocess's stdout/stderr.
//
// Every CLI provider streams its subprocess's output line-by-line via
// bufio.Scanner. Without an explicitly enlarged buffer, bufio.Scanner caps a
// single line at bufio.MaxScanTokenSize (64 KB) — and even the providers that
// did raise stdout's cap only did so to 1 MB, and only for stdout, never
// stderr. Real stream-json output routinely contains a single line well over
// 1 MB (e.g. an assistant message quoting a large file a tool Read/Wrote), so
// this cap must be generous. 8 MB comfortably covers that case while still
// bounding worst-case memory use per scanning goroutine.
const maxScanLineBytes = 8 * 1024 * 1024

// scanLines reads r line-by-line (splitting the same way bufio.Scanner's
// default ScanLines does) via an enlarged buffer (see maxScanLineBytes),
// invoking fn for each line. It returns the scanner's terminal error: nil on
// a clean EOF, or the *bufio.Scanner error (notably bufio.ErrTooLong when a
// single line exceeds maxScanLineBytes) otherwise.
//
// This exists to fix a bug affecting every CLI provider (claude/codex/
// opencode/qwen): scanner.Buffer(...) alone does not surface a signal when a
// line exceeds the cap — Scan() just returns false and the scanning
// goroutine exits silently, dropping the rest of that stream with no log
// entry. Worse, nothing was draining the pipe anymore, so if the child kept
// writing it could block on a full pipe and the run would not actually end
// until the outer run timeout (default 600s) fired — a silently truncated
// log presenting as a mysterious hang.
//
// scanLines fixes both halves of that: callers can check the returned error
// and surface a visible warning (and fail the run) instead of pretending
// nothing happened, and — critically — on a non-nil error scanLines keeps
// draining r to EOF via io.Copy before returning, so the subprocess's pipe
// never backs up and cmd.Wait() can still complete promptly.
func scanLines(r io.Reader, fn func(line string)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScanLineBytes)
	for scanner.Scan() {
		fn(scanner.Text())
	}
	err := scanner.Err()
	if err != nil {
		// Keep draining the pipe so the child process can never block on a
		// full pipe after we stop scanning it — otherwise the run would sit
		// until the outer timeout instead of ending promptly.
		_, _ = io.Copy(io.Discard, r)
	}
	return err
}
