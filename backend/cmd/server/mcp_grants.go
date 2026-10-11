package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/myinisjap/agent-task-editor/backend/internal/remotemcp"
)

const mcpGrantsUsage = `usage:
  server mcp-grants list
  server mcp-grants revoke <grant-id>
  server mcp-grants revoke-email <email>
`

// runMCPGrants implements the `mcp-grants` subcommand: list and revoke remote
// MCP sign-ins. It returns the process exit code.
func runMCPGrants(db *sql.DB, args []string) int {
	ctx := context.Background()
	switch {
	case len(args) == 1 && args[0] == "list":
		grants, err := remotemcp.ListGrants(ctx, db)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		for _, g := range grants {
			state := "active"
			if g.Revoked {
				state = "revoked"
			}
			fmt.Printf("%s\t%s\t%s\tcreated %s\tlast refreshed %s\n", g.ID, state, g.Email, g.CreatedAt, g.LastUsedAt)
		}
		return 0
	case len(args) == 2 && args[0] == "revoke":
		ok, err := remotemcp.RevokeGrant(ctx, db, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "no active grant with that id")
			return 1
		}
		fmt.Println("revoked")
		return 0
	case len(args) == 2 && args[0] == "revoke-email":
		n, err := remotemcp.RevokeEmail(ctx, db, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Printf("revoked %d grant(s)\n", n)
		return 0
	}
	fmt.Fprint(os.Stderr, mcpGrantsUsage)
	return 2
}
