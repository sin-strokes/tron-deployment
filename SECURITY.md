# Security Policy

## Supported Versions

| Version | Supported |
|---|---|
| 0.1.x (alpha) | Yes |

## Reporting a Vulnerability

If you discover a security vulnerability in trond, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

### Reporting Channel

Email: **security@tron.network**

Include:
- Description of the vulnerability
- Steps to reproduce
- Affected version(s)
- Impact assessment (if known)

### Response SLA

| Stage | Timeline |
|---|---|
| Acknowledgment | Within 48 hours |
| Initial assessment | Within 5 business days |
| Fix or mitigation | Within 30 days for critical issues |

### What to Expect

1. We will acknowledge your report within 48 hours
2. We will provide an initial assessment of the issue
3. We will work on a fix and coordinate disclosure
4. You will be credited in the security advisory (unless you prefer anonymity)

## Security Design

trond incorporates the following security measures:

- **Private key protection**: Witness private keys are passed via environment variables, never stored in intent files. The `PrivateKey` type redacts values in all string representations and JSON serialization.
- **SSH command whitelist**: Only pre-approved commands are executed over SSH connections.
- **Audit log**: All mutating operations (apply, stop, start, remove, upgrade, rollback, network create/destroy) are logged to `~/.trond/audit.log` in append-only JSONL format.
- **Confirmation gates**: Destructive operations (remove, destroy) require explicit `--confirm` flags.
- **State file permissions**: State and audit files are created with restricted permissions (0600/0700).

## Scope

The following are in scope for security reports:

- Command injection via intent files or CLI arguments
- Private key leakage through logs, output, or state files
- SSH connection security issues
- Privilege escalation via trond commands
- State file tampering leading to unintended deployments

Out of scope:
- Security of the java-tron node software itself (report to [java-tron](https://github.com/tronprotocol/java-tron))
- Vulnerabilities in Docker or systemd
- Social engineering
