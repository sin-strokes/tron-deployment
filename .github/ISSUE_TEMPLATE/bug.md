---
name: Bug report
about: Something works incorrectly
title: '[BUG] '
labels: bug
---

## What you ran

```bash
$ trond <command> --output=json ...
```

## Expected vs actual

- Expected: ...
- Actual: ...

## Output

<details>
<summary>Full command output</summary>

```
(paste -o json output here, including any error code/message)
```

</details>

## Environment

- `trond version` →
- OS: (`uname -a`)
- Docker / Podman version (if relevant): (`docker --version`)
- java-tron image: (`tronprotocol/java-tron:???`)

## Reproducer

The smallest `intent.yaml` that reproduces this, redacted of secrets:

```yaml
name: ...
network: ...
target: { ... }
nodes: [ ... ]
```

## What you've already tried
