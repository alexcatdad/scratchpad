# Local AI provider verification

Scratchpad v0.1.3 does not yet implement AI processing or embeddings. These checks verify the intended local provider separately from application integration.

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

The owner replaced the embedding package with Qwen3 Embedding 4B GGUF Q8_0 and loaded it as `text-embedding-qwen3-embedding-4b`, with an 8,192-token context. LM Studio reports this package as an embedding model. An explicit request using that identifier from the running Scratchpad Docker container returned HTTP 200, two vectors of 2,560 dimensions, all finite and nonzero. The earlier failed MLX package remains historical evidence, not the selected provider. Qwen3.8 remains the LLM choice. This verifies provider compatibility; Scratchpad application integration is still unimplemented.

## Reload and development application retest — 1 October 2026

After the owner reloaded the selected embedding model, the existing Docker container again received HTTP 200 with two finite, nonzero 2,560-dimensional vectors. The development application also passed an authenticated test against the real provider using a disposable SQLite database: five synthetic captures produced five persisted, nonzero 2,560-dimensional embeddings, and semantic search returned five source-linked matches with finite scores. This verifies the new application integration separately from the running v0.1.3 deployment. The live owner's database was not used for this fixture.
