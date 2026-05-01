# Release Signatures

Every trond release is signed. This page covers what the signature
proves, why we use the scheme we do, and how to verify it.

## TL;DR

```bash
TAG=v0.1.0
BASE=https://github.com/tronprotocol/tron-deployment/releases/download/$TAG

curl -LO "$BASE/checksums.txt"
curl -LO "$BASE/checksums.txt.sig"
curl -LO "$BASE/checksums.txt.pem"

cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature   checksums.txt.sig \
  --certificate-identity-regexp "^https://github\.com/tronprotocol/tron-deployment/\.github/workflows/release\.yml@refs/tags/" \
  --certificate-oidc-issuer     https://token.actions.githubusercontent.com \
  checksums.txt

sha256sum --check --ignore-missing checksums.txt
```

If both commands exit 0, your downloaded artifact came from a tagged
run of this repository's `release.yml` workflow.

## What "keyless OIDC" actually means

Traditional code signing pipelines have a private key that signs every
release. Whoever holds the key can sign anything. Lose the key →
attacker forges releases. Rotate the key → you have to redistribute the
public key to every user.

Sigstore inverts the model:

1. The CI runner already has a workload identity (GitHub Actions
   issues a JWT-shaped OIDC token tied to the workflow file, repo, and
   ref). That token says "I am `release.yml@refs/tags/v0.1.0` running
   in `tronprotocol/tron-deployment`".
2. The runner sends that OIDC token to **Fulcio**, Sigstore's CA.
   Fulcio verifies the token with the issuer (GitHub) and issues a
   short-lived (10-minute) X.509 certificate whose Subject Alternative
   Name field contains the workflow identity from the token.
3. cosign uses the certificate's private key (held only in memory on
   the runner) to sign `checksums.txt`. The signature plus the cert
   are uploaded as `.sig` and `.pem` next to the file.
4. The signature event is also written to **Rekor**, an append-only
   transparency log run by Sigstore. Anyone can later query Rekor and
   prove the signature existed at a given time.
5. The certificate's private key is destroyed when the runner shuts
   down. There is no long-lived signing material to steal.

When you verify, you check three things implicitly:

- The signature is valid for the certificate's public key.
- The certificate was issued by Fulcio (Sigstore root, distributed via
  TUF) for an identity matching `--certificate-identity-regexp`.
- The Rekor entry's timestamp is within the certificate's validity
  window, so the signature was actually produced when the cert was
  live.

That last bit is why an "expired" 10-minute certificate is fine months
later — verification doesn't ask "is the cert valid now", it asks "was
this signature produced while the cert was valid", which Rekor's
timestamp answers offline.

## What the signature does — and doesn't — prove

**Proves**:

- The artifact's SHA256 is exactly what `release.yml` produced when run
  on a tag matching `vX.Y.Z` in `tronprotocol/tron-deployment`.
- The signing event is recorded in a public, append-only log. Tamper
  attempts are detectable.
- An attacker who steals a maintainer's GitHub credentials and pushes
  a malicious tag still leaves a Rekor record with that exact identity.

**Does not prove**:

- That the source code is bug-free.
- That the GitHub Actions workflow was unmodified at build time
  (mitigated by SLSA provenance — see below).
- That GitHub's OIDC issuer or Sigstore's Fulcio CA haven't been
  compromised (single points of trust, but heavily monitored).
- That the upstream Go toolchain (which built trond) was clean. We
  pin Go 1.25.9 by SHA256 in `scripts/bootstrap-go.sh`, but the Go
  team's signing chain is a separate trust decision.

If you need stronger guarantees, layer SLSA provenance on top — covered
later in this doc.

## Why this scheme vs. the alternatives

| Scheme | Key management | Revocation | Audit log | Fork-friendly |
|---|---|---|---|---|
| **cosign keyless OIDC** | none | not needed (10-min cert) | Rekor (public) | yes — verifying needs no setup |
| cosign with a static key | keep `cosign.key` + password in CI secrets | hard (no CRL); rotate + republish public key | optional | needs the public key shipped separately |
| GnuPG (`gpg --sign`) | maintain key, distribute public via keyservers | WoT + revocation cert | none | confusing for end users |
| Apple/Microsoft code signing | CA-issued, costs money | OCSP/CRL (closed network) | none | OS-specific |
| Unsigned tarball + `sha256sum` | n/a | n/a | none | trivially MITM-able |

`cosign-installer@v3` is the GitHub Action that wires this up; it's
maintained by Sigstore directly and used by Kubernetes, Helm, Istio,
Argo CD, kubectl, kind, and most Cloud Native Computing Foundation
projects. Sigstore became a CNCF Graduated project in 2024, putting it
in the same tier as Kubernetes itself.

## How to pin verification harder

The `--certificate-identity-regexp` we use anchors at
`refs/tags/` but accepts any tag. Tighten it for a specific tag:

```bash
cosign verify-blob \
  --certificate-identity "https://github.com/tronprotocol/tron-deployment/.github/workflows/release.yml@refs/tags/v0.1.0" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ...
```

Notice no `-regexp` — exact match. Useful in pinned production rollouts
that need to reject anything not built from a known-good tag.

## SLSA provenance (the next layer)

Keyless OIDC proves the workflow ran. SLSA provenance proves *what
inputs* the workflow consumed: the source commit, the dependency
graph, the builder identity, the env. Adding it later is a one-line
change in `.goreleaser.yaml`:

```yaml
attestations:
  - artifacts: all
    type: slsaprovenance
```

Verification then uses `cosign verify-attestation` with the
`slsa-provenance` predicate type. We've left this off for now to keep
the release pipeline tight; it's the natural follow-up if downstream
distros (Debian, Fedora) start asking for SLSA L3 evidence.

## Common verification errors

`Error: signature does not match` — your downloaded `.tar.gz` doesn't
match the SHA256 in `checksums.txt`. Re-download or check for transport
corruption. Don't pass `--insecure-ignore-tlog` to suppress the error;
that defeats the entire point.

`Error: no matching signatures found` — the `.sig` / `.pem` is from a
different release. Check you grabbed all three from the same `$TAG`.

`Error: Fulcio root not in TUF`  — your `cosign` is too old (< v2.0)
and missing the Sigstore root rotation. Upgrade.

`Error: certificate identity does not match` — the workflow identity
in the certificate doesn't match the regex you supplied. The
human-readable identity is on `cosign verify-blob --output-certificate`
output; double-check repo path and workflow filename.

## Reproducing verification offline

The signature, certificate, and Rekor inclusion proof are all embedded
in `checksums.txt.sig` (a bundle, not just a raw signature). Once
verified, you can keep these three files alongside the tarball
indefinitely; verification works against a stored copy of the Sigstore
TUF root with no live network call to Sigstore. Useful for air-gapped
deploys.

## See also

- Sigstore docs: <https://docs.sigstore.dev/>
- SLSA spec: <https://slsa.dev/>
- The transparency log entry for any trond signature can be looked up
  at <https://search.sigstore.dev/> using the artifact's SHA256.
