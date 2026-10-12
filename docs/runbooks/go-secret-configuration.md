# Go provider configuration and retained secret keys

The development Go backend now implements `GET/PATCH /api/v1/ai/settings` and `POST /api/v1/ai/test`. The existing TypeScript application remains the supported runtime until issue 36 passes complete parity and delivery acceptance. These instructions describe the Go implementation; they do not switch a deployed service.

AI configuration retains existing defaults, validation, numeric versions/`If-Match`, write-only token updates and safe before/after audits. Omit `apiKey` to preserve it, submit an empty string to remove it, or supply a new token to replace it. Configuration responses expose `apiKeyConfigured` and `secretProtection`, never token values or ciphertext. Key-only changes increment the persisted dispatch generation. Provider verification uses the existing synthetic embedding/completion flow; it validates bounded output, dimensions, indices, nonzero vectors and structured JSON. Request admission checks current authorization and generation under the write lock, releases the lock before waiting for the provider and stops subsequent requests/results after configuration changes. Redirects and vendor error bodies do not expose tokens.

## Installation key

Set `SCRATCHPAD_ENCRYPTION_KEY_FILE` to a protected file containing a base64-encoded 32-byte installation key, optionally ending with a newline. The file must be regular and readable by the application, with no access for other users and no group write permission. Owner-only `0600`/`0400` or controlled-group read-only `0440` files are supported. Projected Kubernetes secret symlinks are followed, and the opened target is checked. The application reads this key at startup and never creates or replaces it.

Provision a key once in an existing private directory. These operator commands prevent overwriting an existing key and keep its value out of terminal output:

```sh
set -C
umask 077
openssl rand -base64 32 > '/absolute/private/scratchpad.key'
```

Supply the path to the Go process:

```sh
SCRATCHPAD_ENCRYPTION_KEY_FILE='/absolute/private/scratchpad.key' \
  /absolute/path/scratchpad-server serve
```

Preserve this same key across restarts and replacements. Keep it separate from the database and its backups. Do not paste it into records, issues, logs or committed deployment files. The encrypted representation uses AES-256-GCM, a random nonce and associated data binding its version, entity kind/ID and secret field. Public-key credentials and hashed authentication capabilities retain their existing representation; this is not whole-database or capture encryption.

## Containers and Kubernetes

Mount the retained file read-only and set the same environment variable. For a Go service in Compose, the interface is:

```yaml
services:
  scratchpad:
    environment:
      SCRATCHPAD_ENCRYPTION_KEY_FILE: /run/secrets/scratchpad.key
    volumes:
      - /absolute/private/scratchpad.key:/run/secrets/scratchpad.key:ro
```

The mounted file must be readable by the configured container UID or a controlled group, without world permissions. Preserve the host file rather than generating a fresh key during container creation.

For Kubernetes, provision an owner-controlled Secret from the existing key file without putting its contents in a manifest:

```sh
kubectl -n scratchpad create secret generic scratchpad-encryption-key \
  --from-file=installation-key='/absolute/private/scratchpad.key'
```

A projected mount can use a controlled read-only group. Adapt the UID/group to the actual Go workload:

```yaml
spec:
  securityContext:
    runAsUser: 65532
    runAsGroup: 65532
    fsGroup: 65532
  containers:
    - name: scratchpad
      env:
        - name: SCRATCHPAD_ENCRYPTION_KEY_FILE
          value: /run/secrets/scratchpad/installation-key
      volumeMounts:
        - name: encryption-key
          mountPath: /run/secrets/scratchpad
          readOnly: true
  volumes:
    - name: encryption-key
      secret:
        secretName: scratchpad-encryption-key
        defaultMode: 288 # 0440
```

Keep the same Secret contents across Pod replacement. These are deployment examples, not evidence of a real Compose/Kubernetes rollout; native tests verify the projected-symlink and group-read file behavior.

## Conversion and recovery

Schema migration does not require a usable encryption key. During Go server startup, a usable supplied key converts legacy plaintext provider tokens in a separate atomic transaction, increments configuration version/generation and adds a safe conversion audit. Restart does not repeat completed conversion. Without a usable key, `secretProtection.status` reports `pending_conversion` for legacy plaintext. Encrypted values with an unavailable key or invalid ciphertext report `unavailable`; secret-dependent provider calls return `SECRET_UNAVAILABLE` with no plaintext fallback. A failed conversion is reported safely in startup metadata and remains pending. Ordinary memory and providers that need no token remain usable.

Retain the key with your recovery material. Restoring encrypted settings requires the matching key. If it is lost, project history remains intact: provision a deliberately chosen replacement key, authenticate independently, read the current configuration version and re-enter the provider token, or remove it. This is owner-directed credential replacement, not automatic rotation. Existing snapshots may still contain legacy plaintext and remain private; protect off-host backups separately. The key file is not included in database backup or knowledge export.

## Verification

`GET /api/v1/settings` reports effective `aiEnabled` from current provider configuration. `PATCH /settings` accepts `{expectedVersion, settings: {aiEnabled?, defaultProjectSettings?}}`. Initial global settings require explicit version zero; later updates retain positive versions/`If-Match`. Global policy, provider enablement/generation and both audits commit together. New normal projects inherit the defaults; existing projects retain consent and external projects use conservative defaults. Global settings responses/audits include only policy and entity metadata, never legacy provider-secret fields. A direct provider update is reflected by subsequent global reads.

The real stdio initialization instructions recommend synthetic references such as `secret://provider/main` and exclude API tokens, passwords, private keys and installation keys from captures. This is guidance for the consuming agent, not a promise of automatic redaction or whole-record encryption.

```sh
npm run test:backend
npx playwright test tests/go-passkeys.spec.ts tests/go-github.spec.ts
```

Supply disposable `TEST_POSTGRES_URL` to include PostgreSQL. HTTP/provider tests cover secret persistence/restart, private SQLite snapshot absence of newly stored plaintext, missing/wrong keys, legacy pending/conversion, projected mounts, tampered ciphertext, lost-token replacement, strict versions/configuration, token-free providers and configuration changes during blocked provider calls. Global settings now control AI enablement atomically with provider configuration, and MCP initialization includes secret-reference guidance. Broader workers, PostgreSQL operational restore and RAG remain issue-36 requirements.
