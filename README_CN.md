# trond — TRON 节点部署 CLI

声明式部署、管理和诊断 [java-tron](https://github.com/tronprotocol/java-tron) 节点的命令行工具。

> **历史用户须知**：本仓库以前只提供三份 java-tron HOCON 模板（`main_net_config.conf`/`test_net_config.conf`/`private_net_config.conf`）供手工编辑。这三份**仍在原位、仍跟上游同步**（见下方[配置模板](#配置模板)）。新增的是 `trond` —— 一个 CLI，吃同样的模板加一份小巧的 `intent.yaml`，端到端渲染、部署、管理节点。只需要原始 `.conf` 文件的话，照旧拷贝就行；想跳过手工编辑就往下读。

## 特性

- **声明式部署** —— YAML 描述目标状态，`trond` 负责实现
- **双运行时** —— Docker Compose 或原生 Jar + systemd
- **双目标** —— 本地或远程 SSH
- **幂等** —— `trond apply` 可重复运行
- **Plan/Apply 工作流** —— 部署前预览变更
- **结构化输出** —— JSON 输出供 CI / AI agent 使用
- **内置诊断** —— 同步状态、对等节点数、磁盘、端口、版本检查
- **知识库** —— 内嵌部署指引和故障排除文档
- **测试 SDK** —— 给上层测试工具消费的部署原语

## 安装

### 一键安装

```bash
curl -fsSL https://raw.githubusercontent.com/tronprotocol/tron-deployment/master/scripts/install.sh | sh
```

### Homebrew（macOS / Linux）

```bash
brew install tronprotocol/tap/trond
```

### Docker

```bash
docker run --rm -v ~/.trond:/home/trond/.trond \
  -v /var/run/docker.sock:/var/run/docker.sock \
  tronprotocol/trond:latest --help
```

### 从 release 下载

到 [Releases](https://github.com/tronprotocol/tron-deployment/releases) 下载对应平台的 tar.gz / .deb / .rpm。

### 从源码构建

```bash
git clone https://github.com/tronprotocol/tron-deployment.git
cd tron-deployment
make build       # 产物在 ./bin/trond
```

需要 Go 1.25+。

## 快速开始

```bash
# 1. 拷贝示例
cp examples/mainnet-fullnode.yaml my-node.yaml

# 2. 校验
trond config validate my-node.yaml

# 3. 预览
trond plan --intent my-node.yaml

# 4. 部署
trond apply --intent my-node.yaml --auto-approve --wait

# 5. 状态
trond status my-node
trond health my-node
trond diagnose my-node
```

## 命令速览（共 32 条）

| 类别 | 命令 |
|---|---|
| **生命周期** | `apply`/`deploy` · `plan` · `stop` · `start` · `restart` · `remove` · `upgrade` · `rollback` |
| **配置** | `config validate` · `config render` · `config diff` · `config docs` |
| **观测** | `status` · `list` · `logs` · `health` · `diagnose` · `verify` · `inspect` · `events` |
| **测试 SDK** | `exec` · `files put`/`files get` · `wait` |
| **混沌** | `disconnect` · `connect` · `partition` · `heal` |
| **网络** | `network create` · `network add` · `network status` · `network destroy` |
| **环境** | `preflight` · `bootstrap` |
| **知识库** | `knowledge` |
| **工具** | `doctor` · `version` · `completion` |

完整字段说明见英文版 README 的 "Intent Reference" 章节。

## 私有网络快速部署

```bash
docker network create tron-pn-mesh

cat > pn.yaml <<'EOF'
name: pn
network: private
target: {type: local, runtime: docker, auto_ports: true}
nodes:
  - type: witness
    image: tronprotocol/java-tron
    resources: {memory: 4GB}
    witness_key: {private_key_env: SR_KEY}
    networks: [tron-pn-mesh]
    network_overrides: {p2p_version: 88888, discovery: false, need_sync_check: false}
  - type: fullnode
    image: tronprotocol/java-tron
    resources: {memory: 4GB}
    networks: [tron-pn-mesh]
    network_overrides: {p2p_version: 88888, discovery: false, need_sync_check: false}
EOF

SR_KEY=da146374a75310b9666e834ee4ad0866d6f4035967bfc76217c5a495fff9f0d0 \
  trond network create --intent pn.yaml -o json
```

trond 会自动给 fullnode 配 `node.active=["pn-node0:<p2p>"]` 让它们对等。

## 配置模板

trond 渲染时使用的 java-tron 基础配置：

| 文件 | 网络 | 上游 |
|---|---|---|
| `main_net_config.conf` | 主网 | https://github.com/tronprotocol/java-tron/blob/develop/framework/src/main/resources/config.conf |
| `test_net_config.conf` | Nile 测试网 | https://github.com/tron-nile-testnet/nile-testnet/blob/master/framework/src/main/resources/config-nile.conf |
| `private_net_config.conf` | 私有网络 | 仓库内维护 |

刷新：
```bash
make sync-templates
```

## 文档

- 英文 README：完整的 Intent Reference 字段表 + Test-harness Integration 段
- 内置知识：`trond knowledge`（6 个主题：node-types / troubleshooting / best-practices / cloud-deployment / config-reference / test-harness）
- HOCON 配置项查询：`trond config docs <key>`

## 贡献

见 [CONTRIBUTING.md](CONTRIBUTING.md)。安全问题请按 [SECURITY.md](SECURITY.md) 流程处理。

## License

MIT — 见 [LICENSE](LICENSE)。
