# Scratchpad API client

Generated from the repository OpenAPI 3.1 contract. Use the instance origin as
`baseUrl`; paths include `/api/v1`. Authentication requires a token from the existing
SSH challenge flow, or an existing browser cookie and the configured Origin.

```ts
import { createScratchpadClient } from "@scratchpad/api-client";
const client = createScratchpadClient({
  baseUrl: "http://localhost:3000",
  token: process.env.SCRATCHPAD_TOKEN,
});
const { data, error, response } = await client.GET("/api/v1/projects");
if (error) throw new Error(`${response.status}: ${error.error.message}`);
console.log(data?.projects);
```

Request options, success data and API error envelopes are typed. HTTP errors return
`error`; transport failures reject. Credential enrollment, token renewal, retries
and project AI consent remain explicit caller/server responsibilities.

Run `npm run sdk:generate`, `npm run sdk:check`, `npm run sdk:test`, and
`npm run sdk:build` from the Scratchpad root. Pack with
`npm pack --workspace @scratchpad/api-client --pack-destination /tmp`, then install
the resulting tarball in a consuming project. See the repository
`docs/runbooks/api-clients.md` for Go usage and integration verification.
