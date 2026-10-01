---
title: Optional AI & semantic memory
description: Configure a compatible provider, process permitted projects, review derived evidence and prepare private documents.
---

The source checkout now includes optional AI and semantic memory. The published **v0.1.3** release predates these features; build the current source or install a later release that explicitly includes them. Capture, exact search and project history continue to work without a model.

## Connect a provider

Open **Settings → Optional AI** and configure the OpenAI-compatible base URL, completion model, embedding model and embedding dimensions. Enable AI, save the settings, then use **Test saved provider**. The test sends only synthetic connectivity input.

For the selected local LM Studio setup:

| Setting                           | Value                                 |
| --------------------------------- | ------------------------------------- |
| Provider from Docker on this Mac  | `http://host.docker.internal:1234/v1` |
| Provider from a native Mac server | `http://localhost:1234/v1`            |
| LLM model                         | `qwen/qwen3.8-27b`                    |
| Embedding model                   | `text-embedding-qwen3-embedding-4b`   |
| Embedding dimensions              | `2560`                                |

Use the identifiers and dimensions actually returned by your provider. A downloaded or loaded model alone does not prove embedding support. Provider API keys are optional for local providers; saved keys are not displayed, included in knowledge exports or written into configuration audit snapshots.

Choose an analysis interval, similarity threshold and enabled analysis categories. Provider requests default to a 600-second timeout with reasoning effort **None**. The timeout is configurable from 5 to 3,600 seconds. Choose **Provider default** if your model does not support a reasoning-effort override. Set **Maximum output tokens** to bound response size (256–32,768; default 4,096). The selected Qwen 27B model generated approximately 12 tokens per second during initial local verification, but fell below 2 tokens per second under memory pressure with both models loaded. The default is 600 seconds; a longer explicit timeout can accommodate slow local hardware. Allow enough time for your hardware rather than treating a timeout as successful processing. An interval of **0** disables scheduled processing while retaining manual requests. Automatic jobs persist across restarts; failures are visible in Insights and can be retried. An unavailable provider does not make the core server unready.

## Choose participating projects

In **Projects → Project settings**, enable **Allow AI processing for this project**. Enable **Include in cross-project analysis** only if the project's records may participate in that scope. External/client projects default to both off.

Global AI enablement does not override those permissions. Filtering happens before provider requests and similarity scoring. Withdrawing participation also hides older derived results that relied on the excluded sources.

## Analyze and search

Open **Insights**, select one project or **Across participating projects**, then choose **Analyze memory** or **Build embeddings**. Background jobs show progress and failures while you keep browsing.

Analysis can produce summaries, classification help, duplicates, related decisions, contradiction candidates, clusters, recurring patterns and cleanup recommendations. These are derived suggestions: inspect the source buttons, model attribution and original evidence before accepting or rejecting them. Review preserves original captures and adds audited curation; it does not silently approve a human decision, supersede history or delete a duplicate source.

A response may contain useful suggestions alongside an unsupported pattern or duplicate claim. Those insufficiently supported suggestions are omitted with an audited warning shown on the job; valid suggestions remain available. Invalid source IDs, malformed output or revoked participation fail processing rather than becoming knowledge.

After embeddings complete, use **Search by meaning**. Each result includes a similarity score and opens the underlying record. Model or dimension changes require rebuilding a compatible index; exact search in Memory remains available throughout.

## Prepare a private document

Select one project in Insights and choose **Project handoff**, **Architecture summary**, **Decision report**, **Client-facing history**, or **ADR export**. Generate the document, review its linked sources, then choose **Download Markdown**.

The document stays private until you decide to share it. Downloads include source citations and retain distinctions between observations and interpretation. Document generation does not publish the instance or give another person access to your private memory.

Source links use the dashboard's `/?recordId=...` location. They require access to the original instance; sharing a downloaded document does not grant access to its records.

## Agent access

The source MCP adds `semantic_search`, `get_suggestions`, `process_memory`, `get_ai_jobs`, and `generate_document`. See [MCP setup](/scratchpad/guides/mcp/) for project scope and signing configuration. Provider configuration remains server-owned; agents do not need provider keys.

See the [API contract](https://github.com/alexcatdad/scratchpad/blob/main/docs/api.md#66-remaining-product-implementation-extensions) and [development acceptance runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/post-mvp-development.md) for exact interfaces and verification boundaries.

The [machine-readable OpenAPI contract](https://raw.githubusercontent.com/alexcatdad/scratchpad/main/docs/openapi.json) includes provider settings, processing jobs, derived artifacts, review and semantic-search schemas. Completion providers must support structured JSON-schema responses; compatible embeddings alone do not establish completion support.
