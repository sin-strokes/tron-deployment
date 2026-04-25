package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tronprotocol/tron-deployment/internal/knowledge"
	"github.com/tronprotocol/tron-deployment/internal/output"
)

var explainCmd = &cobra.Command{
	Use:   "explain <key>",
	Short: "Explain a HOCON config key",
	Long:  "Look up an important java-tron HOCON config key and return its explanation.",
	Args:  cobra.ExactArgs(1),
	RunE:  runExplain,
}

func init() {
	Cmd.AddCommand(explainCmd)
}

// configExplanations is a quick lookup for common config keys.
var configExplanations = map[string]string{
	"net.type":                           "Network type: mainnet or testnet. Controls address prefix (0x41 for mainnet, 0xa0 for testnet).",
	"node.listen.port":                   "P2P listen port. Default: 18888. All nodes in a network should use the same port.",
	"node.http.fullNodePort":             "HTTP API port for the FullNode. Default: 8090.",
	"node.rpc.port":                      "gRPC API port. Default: 50051.",
	"node.rpc.solidityPort":              "Solidity node gRPC port. Default: 50061.",
	"node.p2p.version":                   "P2P protocol version. Must match the network. Mainnet: 11111.",
	"seed.node":                          "List of seed node addresses (ip:port) for peer discovery.",
	"storage.db.engine":                  "Database engine: LEVELDB or ROCKSDB. ROCKSDB recommended for production.",
	"storage.db.directory":               "Database storage directory. Default: 'database'.",
	"committee.allowCreationOfContracts": "Enable smart contract creation. 1=enabled.",
	"node.active":                        "List of active peer addresses to connect to directly.",
	"node.discovery.enable":              "Enable peer discovery. Default: true.",
	"vm.supportConstant":                 "Enable constant contract calls (eth_call equivalent). Default: false.",
	"node.jsonrpc.httpFullNodeEnable":    "Enable JSON-RPC API on the FullNode. Default: false.",
	"node.metrics.prometheus.enable":     "Enable Prometheus metrics endpoint. Default: false.",
}

func runExplain(cmd *cobra.Command, args []string) error {
	key := args[0]
	outputFmt, _ := cmd.Flags().GetString("output")

	// Direct lookup
	if explanation, ok := configExplanations[key]; ok {
		if outputFmt == "json" {
			return output.WriteJSON(os.Stdout, map[string]any{
				"key":         key,
				"explanation": explanation,
			})
		}
		fmt.Printf("%s\n  %s\n", key, explanation)
		return nil
	}

	// Search in config-reference knowledge
	content, err := knowledge.Get("config-reference")
	if err == nil && strings.Contains(strings.ToLower(content), strings.ToLower(key)) {
		// Found in knowledge base
		if outputFmt == "json" {
			return output.WriteJSON(os.Stdout, map[string]any{
				"key":     key,
				"message": "Found in config-reference knowledge topic",
				"hint":    "Run: trond knowledge config-reference",
			})
		}
		fmt.Printf("Key %q found in config-reference topic.\n", key)
		fmt.Println("Run: trond knowledge config-reference")
		return nil
	}

	// Not found
	fmt.Fprintf(os.Stderr, "Key %q not found. Common keys:\n", key)
	for k := range configExplanations {
		fmt.Fprintf(os.Stderr, "  %s\n", k)
	}
	return nil
}
