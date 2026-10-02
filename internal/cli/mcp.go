package cli

import (
	"errors"

	"github.com/hunknownz/Meerkat/internal/mcp"
	"github.com/hunknownz/Meerkat/internal/web"
)

// mcpServe is injectable for tests.
var mcpServe = mcp.Serve

// cmdMCP runs a read-only stdio MCP server against the running daemon.
// Stdout carries only JSON-RPC; errors go to stderr.
func cmdMCP(env Env, args []string) (int, error) {
	fs, dd := newFlags("mcp")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	dir, err := dataDir(*dd)
	if err != nil {
		return ExitUsage, err
	}
	if err := mcpServe(env.Ctx, env.Stdin, env.Stdout, dir, web.Assets()); err != nil {
		return ExitFailed, errors.New("mcp session ended with an I/O error")
	}
	return ExitOK, nil
}
