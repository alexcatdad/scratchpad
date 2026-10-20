import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { sshNamespace, sshProof } from "../../apps/web/src/lib/ssh-proof.ts";
import { createScratchpadClient as sourceClient } from "./typescript/index.ts";

// Release acceptance uses the installed tarball with the same actual HTTP checks.
const createScratchpadClient: typeof sourceClient = process.env
  .SCRATCHPAD_SDK_TS_MODULE
  ? (await import(process.env.SCRATCHPAD_SDK_TS_MODULE)).createScratchpadClient
  : sourceClient;

// The Go integration test supplies a real server origin and enrolled key.
const externalBaseUrl = process.env.SCRATCHPAD_SDK_TEST_URL;
const keyPath = process.env.SCRATCHPAD_SDK_SIGNING_KEY;
assert(externalBaseUrl, "Run the SDK integration against the Go backend fixture");
assert(keyPath, "The Go SDK fixture must provide its enrolled signing key");
const directory = mkdtempSync(join(tmpdir(), "scratchpad-sdk-"));
const publicKey = readFileSync(`${keyPath}.pub`, "utf8")
  .trim()
  .split(/\s+/)
  .slice(0, 2)
  .join(" ");
try {
  const baseUrl = externalBaseUrl;
  const anonymous = createScratchpadClient({ baseUrl });
  const challenge = await anonymous.POST("/api/v1/auth/mcp/challenge", {
    body: { publicKey },
  });
  assert(challenge.data?.nonce && challenge.data.challengeId);
  const message = join(directory, "challenge");
  writeFileSync(
    message,
    sshProof(
      challenge.data,
      publicKey,
      baseUrl,
      "ssh_login",
    ),
  );
  execFileSync("ssh-keygen", [
    "-Y",
    "sign",
    "-f",
    keyPath,
    "-n",
    sshNamespace,
    message,
  ]);
  const verified = await anonymous.POST("/api/v1/auth/mcp/verify", {
    body: {
      publicKey,
      challengeId: challenge.data.challengeId,
      signature: readFileSync(`${message}.sig`, "utf8"),
    },
  });
  assert(verified.data?.accessToken);
  const token = verified.data.accessToken;
  const client = createScratchpadClient({ baseUrl, token });
  const resolved = await client.POST("/api/v1/projects/resolve", {
    body: {
      context: {
        git: { remote: "https://github.com/example/sdk-fixture.git" },
      },
    },
  });
  assert.equal(resolved.response.status, 200);
  assert(resolved.data?.project?.id);
  const captured = await client.POST("/api/v1/records", {
    body: {
      projectId: resolved.data.project.id,
      record: {
        type: "finding",
        title: "Generated client integration",
        authority: "explicit",
        confidence: "high",
        payload: {
          finding: "Both generated clients call authenticated HTTP routes.",
        },
      },
    },
  });
  assert.equal(captured.response.status, 201);
  assert(captured.data?.record?.id);
  const detail = await client.GET("/api/v1/records/{id}", {
    params: { path: { id: captured.data.record.id } },
  });
  assert.equal(detail.data?.record?.title, "Generated client integration");
  const unauthenticated = await createScratchpadClient({ baseUrl }).GET(
    "/api/v1/projects",
  );
  assert.equal(unauthenticated.response.status, 401);
  assert.equal(unauthenticated.error?.error.code, "AUTH_REQUIRED");
  const missing = await client.GET("/api/v1/records/{id}", {
    params: { path: { id: "missing" } },
  });
  assert.equal(missing.response.status, 404);
  assert.equal(missing.error?.error.code, "RECORD_NOT_FOUND");
  await new Promise<void>((resolve, reject) => {
    const child = spawn("go", ["test", "./...", "-count=1"], {
      cwd:
        process.env.SCRATCHPAD_SDK_GO_DIRECTORY ??
        new URL("./go", import.meta.url),
      stdio: "inherit",
      env: {
        ...process.env,
        SCRATCHPAD_SDK_TEST_URL: baseUrl,
        SCRATCHPAD_SDK_TEST_TOKEN: token,
      },
    });
    child.on("error", reject);
    child.on("exit", (code) =>
      code === 0
        ? resolve()
        : reject(new Error(`Go integration failed: ${code}`)),
    );
  });
  console.log(
    "Generated TypeScript and Go clients passed authenticated HTTP integration and typed failure checks.",
  );
} finally {
  rmSync(directory, { recursive: true, force: true });
}
