# Generated API clients

Scratchpad provides repository TypeScript and Go clients for every operation in
`docs/openapi.json`. They use the instance origin (for example
`http://localhost:3000`); generated paths include `/api/v1`. Health and readiness
remain at `/health` and `/ready`.

These clients do not replace the stdio MCP or enroll credentials. Obtain a bearer
token through an enrolled SSH key and the challenge/verify endpoints. Tokens expire;
create a new authenticated client after obtaining a replacement. Browser clients
can instead use the owner's existing cookie with `credentials: "include"` and the
configured `Origin` for mutations. Never put tokens in source control.

## Generate, check and test

From the repository root, with Node 24 or later, Go 1.25 or later and OpenSSH:

```sh
npm ci
npm run sdk:generate
npm run sdk:check
npm run sdk:test
npm run sdk:build
```

The generator pins [openapi-typescript 7.13.0](https://openapi-ts.dev/introduction)
and [oapi-codegen 2.8.0](https://github.com/oapi-codegen/oapi-codegen/releases/tag/v2.8.0).
Both read the OpenAPI 3.1 source directly. The generator requires TypeScript 5's
compiler API, so its supported latest 5.x patch is pinned to 5.9.3. Application,
documentation and generated-client builds retain TypeScript 7.0.2; compile commands
select their workspace compiler explicitly. Root TypeScript 5.9.3 satisfies the
generator's peer dependency and is not used to downgrade application compilation. TypeScript uses
[openapi-fetch 0.17.0](https://openapi-ts.dev/openapi-fetch/api); Go pins
`github.com/oapi-codegen/runtime v1.7.0`. Generated output must not be hand-edited.
Update the contract, regenerate, then commit both clients together.

`sdk:check` regenerates into temporary storage and rejects drift, then compiles both
clients. `sdk:test` creates an isolated in-memory instance, provisions a disposable
Ed25519 fixture credential, signs a real SSH challenge, obtains a token through generated
HTTP operations and tests both clients against the actual API over TCP. It checks
project resolution, capture/retrieval and typed unauthorized/missing-record errors.
It neither contacts the owner's instance nor uses a fabricated session token.

## TypeScript usage

The repository workspace package is `@scratchpad/api-client`. Build it before use.
For another repository, copy `packages/clients/typescript`, run `npm install` in
that directory, then `npm run build`; consume that local package with a `file:`
dependency. Alternatively build and pack a compiled local tarball:

```sh
npm run sdk:build
npm pack --workspace @scratchpad/api-client --pack-destination /tmp
npm install /tmp/scratchpad-api-client-0.3.0.tgz
```

Published v0.3.0 and later client releases include `scratchpad-api-client-X.Y.Z.tgz`
under the GitHub release assets and `checksums.txt`. Download and verify the
checksums before installing that tarball. The Go client uses the matching
`packages/clients/go/vX.Y.Z` module tag; consumers can run:

```sh
go get github.com/alexcatdad/scratchpad/packages/clients/go@v0.3.0
```

These are the v0.3.0 delivery instructions; publication is tracked separately in
the optional readiness ledger. Release acceptance installs the published tarball
and remote Go module into independent temporary projects, then repeats the real
SSH-authenticated HTTP integration with those installed implementations:

```sh
RELEASE_TAG=v0.3.0 bash scripts/verify-released-clients.sh
```

The tarball contains JavaScript, declarations and package metadata; npm installs
its pinned transport dependency. No package registry publication is required.

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

Paths, parameters, request bodies, success payloads and API errors are generated
types. Network failures reject the promise; HTTP failures return a typed `error`
with the raw `Response` for status and headers. Supply `fetch` for a custom transport
or request cancellation through the generated request options. The library does not
log credentials, retry mutations automatically or relax server authorization.

## Go usage

The separate module is
`github.com/alexcatdad/scratchpad/packages/clients/go`. Until selecting a remote
repository revision, a consuming project's `go.mod` can use a local replacement:

```go
require github.com/alexcatdad/scratchpad/packages/clients/go v0.0.0
replace github.com/alexcatdad/scratchpad/packages/clients/go => /path/to/scratchpad/packages/clients/go
```

```go
client, err := scratchpad.NewAuthenticatedClient("http://localhost:3000", scratchpad.Auth{
    Token: os.Getenv("SCRATCHPAD_TOKEN"),
    HTTPClient: &http.Client{Timeout: 30 * time.Second},
})
if err != nil { return err }
result, err := client.GetProjectsWithResponse(ctx)
if err != nil { return err } // transport/JSON failure
if result.JSONDefault != nil {
    return &scratchpad.ResponseError{Status: result.StatusCode(), Detail: *result.JSONDefault}
}
if result.StatusCode() != http.StatusOK || result.JSON200 == nil {
    return fmt.Errorf("unexpected HTTP %d", result.StatusCode())
}
```

Import the module as `scratchpad`; standard-library imports above are `os`,
`net/http`, `time` and `fmt`. Generated `WithResponse` methods expose typed status
payloads, typed default API failures, body bytes and response headers. Pass a
context with a deadline, or provide an `HTTPClient` with a timeout. Cookie callers
can provide a cookie-jar HTTP client and `Auth.Origin`. The Go module and MCP's
existing Go SDK have independent dependency boundaries.
