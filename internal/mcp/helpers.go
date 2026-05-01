package mcp

import (
	"fmt"
	"strconv"

	"github.com/tronprotocol/tron-deployment/internal/output"
)

// notFound builds the standard NODE_NOT_FOUND structured error for a
// missing managed node. Tools call this when an MCP-supplied name
// doesn't resolve in state.
func notFound(operation, name string) *output.StructuredError {
	return output.NewError("NODE_NOT_FOUND", output.ExitGeneralError,
		fmt.Sprintf("%s: no managed node named %q", operation, name)).
		WithSuggestions(
			"Call the 'list' tool to see currently-managed nodes",
			"If this is a fresh deployment, call 'apply' first to create the node",
		)
}

// httpURL formats a port into the http://127.0.0.1:<p> URL we surface
// to agents. Agents can re-use this in their own follow-up probes
// (e.g. `wait --http <url>`).
func httpURL(port int) string {
	return "http://127.0.0.1:" + strconv.Itoa(port)
}

// grpcAddr formats the host:port grpc endpoint.
func grpcAddr(port int) string {
	return "127.0.0.1:" + strconv.Itoa(port)
}
