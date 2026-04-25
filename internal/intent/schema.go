package intent

// Intent is the top-level declarative file describing desired node state.
type Intent struct {
	Name    string     `yaml:"name" json:"name" validate:"required,hostname_rfc1123"`
	Target  Target     `yaml:"target" json:"target" validate:"required"`
	Network string     `yaml:"network" json:"network" validate:"required,oneof=mainnet nile private"`
	Nodes   []NodeSpec `yaml:"nodes" json:"nodes" validate:"required,min=1,dive"`
}

// Target specifies where to deploy.
type Target struct {
	Type         string `yaml:"type" json:"type" validate:"required,oneof=local ssh"`
	Host         string `yaml:"host,omitempty" json:"host,omitempty" validate:"required_if=Type ssh"`
	User         string `yaml:"user,omitempty" json:"user,omitempty" validate:"required_if=Type ssh"`
	Port         int    `yaml:"port,omitempty" json:"port,omitempty"`
	IdentityFile string `yaml:"identity_file,omitempty" json:"identity_file,omitempty"`
	Runtime      string `yaml:"runtime,omitempty" json:"runtime,omitempty" validate:"omitempty,oneof=docker jar"`
	// AutoPorts replaces every node port that resolves to a default value
	// (8090, 50051, 18888, …) with a free OS-assigned port. This lets
	// concurrent test enclaves spin up in parallel without manually staging
	// non-overlapping port plans in each intent file.
	AutoPorts bool `yaml:"auto_ports,omitempty" json:"auto_ports,omitempty"`
}

// NodeSpec defines a single node's desired configuration.
type NodeSpec struct {
	Type           string      `yaml:"type" json:"type" validate:"required,oneof=fullnode witness solidity lite"`
	Version        string      `yaml:"version,omitempty" json:"version,omitempty"`
	Image          string      `yaml:"image,omitempty" json:"image,omitempty"`
	InstallPath    string      `yaml:"install_path,omitempty" json:"install_path,omitempty"`
	ProcessManager string      `yaml:"process_manager,omitempty" json:"process_manager,omitempty" validate:"omitempty,oneof=systemd nohup"`
	SystemUser     string      `yaml:"system_user,omitempty" json:"system_user,omitempty"`
	WitnessKeyEnv  string      `yaml:"witness_key_env,omitempty" json:"witness_key_env,omitempty"`
	Features       Features    `yaml:"features,omitempty" json:"features,omitempty"`
	Resources      Resources   `yaml:"resources,omitempty" json:"resources,omitempty"`
	JVM            *JVMConfig  `yaml:"jvm,omitempty" json:"jvm,omitempty"`
	Ports          PortMapping `yaml:"ports,omitempty" json:"ports,omitempty"`
}

// Features contains feature flags for a node.
type Features struct {
	Metrics        *bool `yaml:"metrics,omitempty" json:"metrics,omitempty"`
	JSONRPC        *bool `yaml:"jsonrpc,omitempty" json:"jsonrpc,omitempty"`
	RateLimit      *bool `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`
	EventSubscribe *bool `yaml:"event_subscribe,omitempty" json:"event_subscribe,omitempty"`
	PBFT           *bool `yaml:"pbft,omitempty" json:"pbft,omitempty"`
}

// Resources specifies resource constraints.
type Resources struct {
	Memory  string `yaml:"memory,omitempty" json:"memory,omitempty"`
	Storage string `yaml:"storage,omitempty" json:"storage,omitempty"`
}

// JVMConfig provides optional JVM tuning overrides.
type JVMConfig struct {
	HeapMax      string `yaml:"heap_max,omitempty" json:"heap_max,omitempty"`
	HeapNew      string `yaml:"heap_new,omitempty" json:"heap_new,omitempty"`
	DirectMemory string `yaml:"direct_memory,omitempty" json:"direct_memory,omitempty"`
	GC           string `yaml:"gc,omitempty" json:"gc,omitempty" validate:"omitempty,oneof=G1 CMS auto"`
	GCLog        *bool  `yaml:"gc_log,omitempty" json:"gc_log,omitempty"`
}

// PortMapping defines custom port overrides.
type PortMapping struct {
	HTTP         int `yaml:"http,omitempty" json:"http,omitempty"`
	GRPC         int `yaml:"grpc,omitempty" json:"grpc,omitempty"`
	SolidityHTTP int `yaml:"solidity_http,omitempty" json:"solidity_http,omitempty"`
	SolidityGRPC int `yaml:"solidity_grpc,omitempty" json:"solidity_grpc,omitempty"`
	JSONRPC      int `yaml:"jsonrpc,omitempty" json:"jsonrpc,omitempty"`
	P2P          int `yaml:"p2p,omitempty" json:"p2p,omitempty"`
	Metrics      int `yaml:"metrics,omitempty" json:"metrics,omitempty"`
}

// BoolPtr is a helper for creating *bool values in intent construction.
func BoolPtr(v bool) *bool {
	return &v
}
