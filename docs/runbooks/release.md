# Native, container, and Homebrew release

## Status and authority

The release pipeline is implemented in `.github/workflows/release.yml`. It has not yet published or notarized a Scratchpad release. A green ordinary CI run does not establish signing, Apple acceptance, registry publication, or tap installation.

The PRD requires four native MCP targets, Developer ID signing and notarization for macOS, checksums, GitHub Release assets, GHCR application images, and automatic delivery through the existing `alexcatdad/homebrew-tap` repository. The workflow fails closed when signing credentials or notarization are missing. It never publishes unsigned macOS substitutes.

The existing Paw Proxy GitHub App delivery pattern and USB Boop's published-artifact verification and downgrade prevention were inspected. Scratchpad uses tagged builds; it does not copy their automatic version bumping or assume their credentials are available here.

## One-time administrator configuration

Create the **action-runners** GitHub environment in `alexcatdad/scratchpad`, with suitable release protection. Configure these names through GitHub's encrypted settings; never paste values into a runbook, issue, agent message, or command history:

| Kind     | Name                           | Purpose                                                                                             |
| -------- | ------------------------------ | --------------------------------------------------------------------------------------------------- |
| Variable | `APP_ID`                       | Existing GitHub App ID used by Paw Proxy's tap delivery                                             |
| Secret   | `APP_SECRET`                   | That application's private key; installation must allow contents write on `alexcatdad/homebrew-tap` |
| Secret   | `APPLE_CERTIFICATE_P12_BASE64` | Base64-encoded Developer ID Application certificate and private key exported as P12                 |
| Secret   | `APPLE_CERTIFICATE_PASSWORD`   | Password protecting the P12                                                                         |
| Secret   | `APPLE_SIGNING_IDENTITY`       | Full `Developer ID Application: …` identity                                                         |
| Secret   | `APPLE_ID`                     | Apple account authorized for notarization                                                           |
| Secret   | `APPLE_TEAM_ID`                | Developer team identifier matching the signing certificate                                          |
| Secret   | `APPLE_APP_SPECIFIC_PASSWORD`  | App-specific password for that Apple account                                                        |

`GITHUB_TOKEN` is supplied by Actions. Jobs receive only the read/write scopes they need; the image job needs packages write, and release publication needs contents write. GHCR package visibility and repository linkage must be checked after initial publication before promising anonymous image pulls.

Metadata inspection on 2026-09-29 found no repository secrets and only the `github-pages` environment in Scratchpad. Paw Proxy's `action-runners` environment has `APP_SECRET` and `APP_ID`; those do not automatically transfer across repositories. No secret values were read. USB Boop signs locally, so its configuration is not evidence that Apple signing secrets exist for Scratchpad CI.

The project has not selected a license in the repository. The generated tap formula deliberately does not invent a license declaration. Resolve licensing before representing the project as open source or publishing under a specific license.

## Validate before tagging

1. Ensure every intended change is committed and on `main`.
2. Run the development and infrastructure checks. Run `actionlint`, `shellcheck scripts/release-*.sh`, and `node --test scripts/release-formula.test.mjs` for release changes.
3. Wait for the latest `CI` workflow at the exact intended commit to finish successfully. The release preflight checks that exact SHA; a green run on an earlier commit is insufficient.
4. Choose an unused stable tag matching `vX.Y.Z`. Pre-release tags are deliberately outside this first pipeline.
5. Confirm credentials and tap application access are available. Have the owner authorize the release/tag push before publishing.

Once authorized, tag the reviewed commit and push that tag. The push starts the workflow. A manual dispatch can retry an existing tag; it cannot fabricate an unreviewed source revision. Preflight also requires the tag commit to be reachable from `main`.

## Pipeline behavior

1. Preflight validates the tag, exact-source CI, signing credentials, and GitHub App access to the tap.
2. Matrix jobs build macOS ARM64/AMD64 and Linux ARM64/AMD64 with Go's pinned toolchain and version metadata.
3. macOS jobs import the certificate into an ephemeral keychain, sign with hardened runtime and a secure timestamp, verify the team and signature, and submit the exact ZIP to `notarytool`. Only `Accepted` permits progression. The keychain and certificate files are deleted on exit.
4. The image job builds and pushes `linux/amd64` and `linux/arm64` to `ghcr.io/alexcatdad/scratchpad:vX.Y.Z`, with provenance and SBOM attestations. It starts only after all native jobs succeed.
5. Publication verifies all four archives and both Apple receipts, generates `checksums.txt`, and publishes a draft release only after uploading the complete inventory. Published releases are not overwritten by reruns.
6. The tap job downloads the published assets again, verifies checksums, generates `Formula/scratchpad-mcp.rb`, checks Ruby syntax, and commits it with the existing GitHub App identity. Lower versions cannot replace a higher installed formula version. A conflicting tap push fails instead of force-pushing.

The workflow serializes releases. It publishes versioned image tags only; it does not silently move `latest`. GitHub Releases are also not forced into the latest slot. This keeps recovery of older tags from unexpectedly changing the default version.

## Artifact inventory

For tag `vX.Y.Z`:

- `scratchpad-mcp-vX.Y.Z-darwin-arm64.zip`
- `scratchpad-mcp-vX.Y.Z-darwin-amd64.zip`
- `scratchpad-mcp-vX.Y.Z-linux-arm64.tar.gz`
- `scratchpad-mcp-vX.Y.Z-linux-amd64.tar.gz`
- `notarization-darwin-arm64.json`
- `notarization-darwin-amd64.json`
- `checksums.txt`

Each archive contains `scratchpad-mcp`. Bare Mach-O command-line binaries and ZIP containers cannot receive stapled tickets. The published ZIP is the exact Apple-accepted file; the operating system obtains the notarization ticket online. No offline stapling guarantee is claimed.

Homebrew selects the archive for the host OS and CPU and installs Git/OpenSSH dependencies. Linux Homebrew also covers WSL. Native Windows is not part of this release matrix.

## Recovery and verification

Before a public release exists, rerun the failed jobs after fixing the cause. A draft release may have its assets completed on retry. Never replace a published binary under the same tag.

If the release is already public and only tap delivery failed, dispatch `Release` with that tag and **homebrew_only: true**. This downloads and verifies existing release assets without rebuilding, resigning, notarizing, or republishing the image. Rebuilding an Apple-signed archive would change its checksum and violate release immutability. An older recovery cannot downgrade the formula.

Publication spans GitHub Releases, GHCR, and a second Git repository; it is not atomic. An image can exist even when later release publication fails. Record each outcome separately and recover the incomplete destination. Do not claim complete delivery from the first successful job.

After a successful run, verify:

1. Every named GitHub asset exists and checksums match downloaded bytes.
2. Both macOS receipts are accepted and signatures match the configured team.
3. The GHCR digest contains both architectures and each architecture starts and passes readiness on a matching host or verified emulation.
4. `brew install alexcatdad/tap/scratchpad-mcp` and `brew test alexcatdad/tap/scratchpad-mcp` work on macOS and Linux.
5. `scratchpad-mcp --version` matches the tag, and a fresh real MCP session can authenticate and retrieve a record.

These post-publication checks remain necessary even if the workflow is green.

## Sources and pins

Action tags and commit SHAs were resolved from each official GitHub repository's latest stable release on 2026-09-29. The workflow records both beside every action reference. Dependency updates must repeat that lookup and rerun the infrastructure checks.

Apple's [notarization guidance](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution) and [custom workflow documentation](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow) define Developer ID, hardened runtime, and acceptance requirements. Docker's [multi-platform Actions documentation](https://docs.docker.com/build/ci/github-actions/multi-platform/) covers the image matrix; GitHub's [container publication guidance](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images) describes GHCR token permissions.
