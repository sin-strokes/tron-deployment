package intent

import (
	"fmt"
	"net"
)

// ApplyDefaults fills in default values for fields not explicitly set in the intent.
func ApplyDefaults(intent *Intent) {
	// Target defaults
	if intent.Target.Port == 0 && intent.Target.Type == "ssh" {
		intent.Target.Port = 22
	}
	if intent.Target.Runtime == "" {
		intent.Target.Runtime = "docker"
	}
	if intent.Target.IdentityFile == "" && intent.Target.Type == "ssh" {
		intent.Target.IdentityFile = "~/.ssh/id_rsa"
	}

	for i := range intent.Nodes {
		applyNodeDefaults(&intent.Nodes[i])
	}

	if intent.Target.AutoPorts {
		// Replace every port that's currently at its java-tron default with
		// a free OS-assigned port. Errors here are non-fatal — leaving the
		// default port is the same behavior the user gets without auto.
		_ = allocateFreePorts(intent)
	}
}

// allocateFreePorts walks each node's PortMapping and replaces the standard
// java-tron defaults with OS-assigned free ports. Any port the user set
// explicitly to a non-default value is preserved.
func allocateFreePorts(intent *Intent) error {
	defaults := map[string]int{
		"http":          8090,
		"grpc":          50051,
		"solidity_http": 8091,
		"solidity_grpc": 50061,
		"jsonrpc":       8545,
		"p2p":           18888,
		"metrics":       9527,
	}
	used := make(map[int]bool)

	// pickPort returns a port that is free for BOTH TCP and UDP. The P2P
	// port needs both (java-tron's discovery is UDP), and docker compose
	// fails the whole container when either family can't bind. We don't
	// know up front which port is destined for P2P vs HTTP, so we apply
	// the strict TCP+UDP test to every allocation — slightly wasteful for
	// HTTP-only ports, but never wrong.
	pickPort := func() (int, error) {
		for attempt := 0; attempt < 32; attempt++ {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return 0, fmt.Errorf("listen tcp: %w", err)
			}
			port := l.Addr().(*net.TCPAddr).Port
			l.Close()
			if used[port] {
				continue
			}
			// Verify the same port is free on UDP. Failure means some
			// other process holds the udp socket — try another port.
			udp, uerr := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
			if uerr != nil {
				continue
			}
			udp.Close()
			used[port] = true
			return port, nil
		}
		return 0, fmt.Errorf("could not find a free TCP+UDP port after 32 attempts")
	}

	for i := range intent.Nodes {
		p := &intent.Nodes[i].Ports
		if p.HTTP == defaults["http"] {
			if port, err := pickPort(); err == nil {
				p.HTTP = port
			}
		}
		if p.GRPC == defaults["grpc"] {
			if port, err := pickPort(); err == nil {
				p.GRPC = port
			}
		}
		if p.SolidityHTTP == defaults["solidity_http"] {
			if port, err := pickPort(); err == nil {
				p.SolidityHTTP = port
			}
		}
		if p.SolidityGRPC == defaults["solidity_grpc"] {
			if port, err := pickPort(); err == nil {
				p.SolidityGRPC = port
			}
		}
		if p.JSONRPC == defaults["jsonrpc"] {
			if port, err := pickPort(); err == nil {
				p.JSONRPC = port
			}
		}
		if p.P2P == defaults["p2p"] {
			if port, err := pickPort(); err == nil {
				p.P2P = port
			}
		}
		if p.Metrics == defaults["metrics"] {
			if port, err := pickPort(); err == nil {
				p.Metrics = port
			}
		}
	}
	return nil
}

func applyNodeDefaults(node *NodeSpec) {
	if node.Version == "" {
		node.Version = "latest"
	}
	if node.Image == "" {
		node.Image = "tronprotocol/java-tron"
	}
	if node.InstallPath == "" {
		node.InstallPath = "/opt/tron"
	}
	if node.ProcessManager == "" {
		node.ProcessManager = "systemd"
	}
	if node.SystemUser == "" {
		node.SystemUser = "tron"
	}

	// Feature defaults
	if node.Features.RateLimit == nil {
		node.Features.RateLimit = BoolPtr(true)
	}

	// Resource defaults
	if node.Resources.Memory == "" {
		node.Resources.Memory = "16GB"
	}

	// Port defaults
	if node.Ports.HTTP == 0 {
		node.Ports.HTTP = 8090
	}
	if node.Ports.GRPC == 0 {
		node.Ports.GRPC = 50051
	}
	if node.Ports.SolidityHTTP == 0 {
		node.Ports.SolidityHTTP = 8091
	}
	if node.Ports.SolidityGRPC == 0 {
		node.Ports.SolidityGRPC = 50061
	}
	if node.Ports.JSONRPC == 0 {
		node.Ports.JSONRPC = 8545
	}
	if node.Ports.P2P == 0 {
		node.Ports.P2P = 18888
	}
	if node.Ports.Metrics == 0 {
		node.Ports.Metrics = 9527
	}

	// JVM defaults. GC and GC logging are opt-in: emitting them by default
	// collides with the official java-tron image's start.sh ("Multiple
	// garbage collectors selected") and triggers a container restart loop.
	// Users who want trond-managed GC tuning must set jvm.gc explicitly.
	if node.JVM == nil {
		node.JVM = &JVMConfig{}
	}
	if node.JVM.GC == "" {
		node.JVM.GC = "auto"
	}
}
