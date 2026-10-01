# Local AI provider verification

The installed Scratchpad v0.1.3 predates AI processing. The development application now implements AI and embeddings; distinguish provider smoke checks below from authenticated application acceptance.

## Selected models

- LLM: `qwen/qwen3.8-27b`, explicitly selected by the owner.
- Embeddings: `text-embedding-qwen3-embedding-4b` (Qwen3 Embedding 4B, GGUF Q8_0), verified with 2,560-dimensional vectors from the application container.
- Mac provider URL: `http://localhost:1234/v1`.
- Docker provider URL: `http://host.docker.internal:1234/v1`.

## Check models and load the candidate

Use the model identifier reported by the current server. Downloaded models are not necessarily loaded or supported as embedding models.

```sh
curl --fail --silent http://localhost:1234/api/v1/models
lms ps
lms load text-embedding-qwen3-embedding-4b --identifier text-embedding-qwen3-embedding-4b --context-length 8192 --yes
```

Verify through the running application container using only synthetic text:

```sh
docker compose -p scratchpad-local -f ~/.local/share/scratchpad/compose.yaml exec -T scratchpad node - <<'JS'
const response = await fetch('http://host.docker.internal:1234/v1/embeddings', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({
    model: 'text-embedding-qwen3-embedding-4b',
    input: 'Synthetic Scratchpad embedding smoke test.',
  }),
  signal: AbortSignal.timeout(45000),
});
const body = await response.json();
console.log(JSON.stringify({
  status: response.status,
  error: body.error,
  model: body.model,
  dimensions: body.data?.[0]?.embedding?.length,
  finite: body.data?.[0]?.embedding?.every(Number.isFinite),
}));
if (!response.ok) process.exitCode = 1;
JS
```

Acceptance requires a successful response with a nonempty, finite embedding vector. A model name or successful load alone does not prove support. Once accepted, preserve the embedding model identity and vector dimensions alongside derived data; changing the model requires rebuilding the corresponding embeddings.

Unload only the model loaded for this check if it fails:

```sh
lms unload text-embedding-qwen3-embedding-4b
```

## Observed result — 1 October 2026

The local server lists the downloaded Qwen3 Embedding 4B and 8B DWQ packages as MLX `llm` models. The 4B candidate loaded successfully, but `/v1/embeddings` returned HTTP 400 with `No models loaded` even while `lms ps` showed the candidate loaded. It was then unloaded; the owner's Qwen3.8 LLM remained loaded. The 8B package was not tested. The preferred 4B model needs a package/runtime combination verified through the embedding endpoint before use in Scratchpad.

## Successful retest — 1 October 2026

The owner replaced the embedding package with Qwen3 Embedding 4B GGUF Q8_0 and loaded it as `text-embedding-qwen3-embedding-4b`, with an 8,192-token context. LM Studio reports this package as an embedding model. An explicit request using that identifier from the running Scratchpad Docker container returned HTTP 200, two vectors of 2,560 dimensions, all finite and nonzero. The earlier failed MLX package remains historical evidence, not the selected provider. Qwen3.8 remains the LLM choice. This initial check verifies provider compatibility. The later authenticated application result below establishes embedding integration.

## Reload and development application retest — 1 October 2026

After the owner reloaded the selected embedding model, the existing Docker container again received HTTP 200 with two finite, nonzero 2,560-dimensional vectors. The development application also passed an authenticated test against the real provider using a disposable SQLite database: five synthetic captures produced five persisted, nonzero 2,560-dimensional embeddings, and semantic search returned five source-linked matches with finite scores. This verifies the new application integration separately from the running v0.1.3 deployment. The live owner's database was not used for this fixture.

## Latest provider reload check — 1 October 2026

A fresh three-input request after the owner reloaded the model returned three finite, nonzero 2,560-dimensional vectors in 6.95 seconds. Two paraphrases scored 0.7652 cosine similarity; an unrelated sentence scored 0.2248. This repeat confirms provider health, independently of application acceptance.

## Automated real application fixture

Build the disposable image before running the authenticated real-provider fixture:

```sh
docker build -t scratchpad:ci .
SCRATCHPAD_REAL_AI=1 SCRATCHPAD_E2E_DOCKER=1 npx playwright test tests/real-ai.spec.ts --workers=1
```

The fixture owns its container, volume, synthetic records and passkey. It checks persisted embeddings, semantic retrieval, analysis provenance and a private handoff. Allow slow local generation to finish; inspect the terminal job result before rerunning. Model requests and HTTP transport both need to honor the configured timeout.

After a complete run has already verified actual analysis, rerun embeddings, semantic retrieval and the private handoff without regenerating analysis:

```sh
SCRATCHPAD_REAL_AI=1 SCRATCHPAD_REAL_AI_EXPORT_ONLY=1 SCRATCHPAD_E2E_DOCKER=1 SCRATCHPAD_E2E_IMAGE=scratchpad:ci npx playwright test tests/real-ai.spec.ts --workers=1
```

This mode uses a fresh disposable container and synthetic sources. It does not claim fresh analysis acceptance. Node HTTP transport must allow the configured model deadline; the default dispatcher previously cancelled headers at 300 seconds despite a 600-second request setting. The application now supplies an explicit dispatcher and closes it on completion or abort.

If the selected model reaches its configured deadline under memory pressure, preserve the diagnosis and adjust the existing provider timeout explicitly instead of silently truncating documents. The real fixture accepts a bounded timeout override:

```sh
SCRATCHPAD_REAL_AI=1 SCRATCHPAD_REAL_AI_EXPORT_ONLY=1 SCRATCHPAD_REAL_AI_TIMEOUT_SECONDS=1800 SCRATCHPAD_E2E_DOCKER=1 npx playwright test tests/real-ai.spec.ts --workers=1
```

The application accepts 5–3,600 seconds and keeps the default at 600. Worker leases remain longer than the configured request deadline. An override is not a passing result: inspect completed Markdown, provenance and immutable source comparisons.

## Final real Docker handoff — 1 October 2026

The export-only fixture passed against the final draft-label candidate image `sha256:fdaf3404e07b950c82b3dfb4f397e8a0f629c168ccb842d05c183b26da581716` with an explicit 1,800-second timeout. In 7.9 minutes it persisted five valid 2,560-dimensional vectors, returned five semantic source matches, downloaded a 2,588-character handoff carrying the application draft label and all five source links, opened an authenticated source detail, and preserved every original record hash. Both selected models stayed loaded. The earlier real analysis pass remains separate; this run deliberately repeated embeddings and export only.

## Latest reload and development upgrade

After another owner reload, a request from the running application container returned HTTP 200 with two finite, nonzero 2,560-dimensional Qwen vectors in 1.959 seconds. This is a provider smoke check; the authenticated fixture remains the integration evidence.

The final absolute-citation fixture passed against image `sha256:06a3fb6f71a5fa6cb81049b87fcac0f22471d629858eec9837977497135a40b4` in 6.9 minutes. It downloaded a 2,370-character private handoff with the deterministic draft label, extracted and followed an actual absolute source URL, validated five vectors and five semantic matches, and preserved all original source hashes. Real analysis was already verified separately.

The owner dashboard was upgraded to that development image with its existing Compose project and volume. `/ready` succeeds, and all 27 stored rows exactly match the pre-upgrade online backup. Browser session verification remains pending while the Mac is locked. This image is not a published v0.2.0 release.
