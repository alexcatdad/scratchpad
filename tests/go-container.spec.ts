import { execFileSync, spawn, spawnSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { createServer } from "node:net";
import { expect, test } from "@playwright/test";

for (const scenario of [
  "go-restore",
  "typescript-rollback",
  "go-secret-restore",
  "go-postgres-restore",
  "go-pgvector-restore",
  "go-postgres-secret-restore",
  "typescript-postgres-rollback",
] as const) {
  test(`actual images preserve browser access through ${scenario}`, async ({
    page,
  }) => {
    const context = process.env.TEST_DOCKER_CONTEXT;
    const image = process.env.TEST_GO_IMAGE;
    const retainedImage = process.env.TEST_RETAINED_IMAGE;
    const rollback =
      scenario === "typescript-rollback" ||
      scenario === "typescript-postgres-rollback";
    let databaseURL = "";
    const ragRestore = scenario === "go-pgvector-restore";
    const postgresRestore =
      scenario === "go-postgres-restore" ||
      ragRestore ||
      scenario === "go-postgres-secret-restore" ||
      scenario === "typescript-postgres-rollback";
    test.skip(
      !context || !image || (rollback && !retainedImage),
      "Explicit disposable Docker context and Go image are required",
    );
    const docker = (...args: string[]) =>
      execFileSync("docker", ["--context", context ?? "", ...args], {
        encoding: "utf8",
        timeout: 30_000,
        stdio: ["ignore", "pipe", "pipe"],
        env: {
          ...process.env,
          SCRATCHPAD_DATABASE_URL: databaseURL,
          POSTGRES_PASSWORD: "synthetic-container-database-password",
        },
      }).trim();
    const stop = (id: string, goRuntime: boolean) => {
      docker("stop", "--time", "20", id);
      const code = docker("inspect", "--format", "{{.State.ExitCode}}", id);
      if (!goRuntime && code === "1") {
        const logs = spawnSync(
          "docker",
          ["--context", context ?? "", "logs", id],
          { encoding: "utf8", timeout: 30_000 },
        );
        expect(logs.status).toBe(0);
        expect(`${logs.stdout}${logs.stderr}`).toContain("signal SIGTERM");
      } else expect(goRuntime ? ["0"] : ["0", "143"]).toContain(code);
    };
    const suffix = randomUUID();
    const secretRestore =
      scenario === "go-secret-restore" ||
      scenario === "go-postgres-secret-restore";
    const network = `scratchpad-go-provider-${suffix}`;
    const correctKeyVolume = `scratchpad-go-key-${suffix}`,
      wrongKeyVolume = `scratchpad-go-wrong-key-${suffix}`;
    let selectedKeyVolume = correctKeyVolume;
    let networkCreated = false;
    let databaseContainer = "";
    let providerContainer = "";
    let archive: Buffer | undefined;
    let sourcePassageId = "";
    let sourceProviderCalls = 0;
    const providerCalls = () =>
      JSON.parse(
        docker(
          "exec",
          providerContainer,
          "node",
          "-e",
          "fetch('http://127.0.0.1:8080/stats').then(r=>r.text()).then(console.log)",
        ),
      ).calls as number;
    const sourceVolume = `scratchpad-go-source-${suffix}`,
      restoredVolume = `scratchpad-go-restore-${suffix}`;
    const containers: string[] = [],
      volumes: string[] = [];
    const port = await new Promise<number>((done) => {
      const listener = createServer();
      listener.listen(0, "127.0.0.1", () => {
        const address = listener.address();
        if (!address || typeof address === "string")
          throw new Error("Missing test port");
        listener.close(() => done(address.port));
      });
    });
    const origin = `http://localhost:${port}`;
    let tunnel: ReturnType<typeof spawn> | undefined;
    let tunnelClosed: Promise<void> | undefined;
    const run = (volume: string, selectedImage: string) => {
      const id = docker(
        "run",
        "-d",
        "-v",
        `${volume}:/data`,
        "-p",
        `127.0.0.1:${port}:3000`,
        "-e",
        `SCRATCHPAD_PUBLIC_URL=${origin}`,
        ...(ragRestore ? ["-e", "SCRATCHPAD_RAG_ENABLED=true"] : []),
        ...(postgresRestore || secretRestore ? ["--network", network] : []),
        ...(postgresRestore ? ["-e", "SCRATCHPAD_DATABASE_URL"] : []),
        ...(secretRestore
          ? [
              ...(selectedKeyVolume
                ? ["-v", `${selectedKeyVolume}:/keys:ro`]
                : []),
              "-e",
              "SCRATCHPAD_ENCRYPTION_KEY_FILE=/keys/installation-key",
            ]
          : []),
        selectedImage,
      );
      containers.push(id);
      return id;
    };
    const start = async (volume: string, selectedImage = image ?? "") => {
      const id = run(volume, selectedImage);
      await expect
        .poll(
          async () => {
            try {
              return (await fetch(`${origin}/ready`)).status;
            } catch {
              return 0;
            }
          },
          { timeout: 15_000 },
        )
        .toBe(200);
      await expect
        .poll(async () => (await fetch(origin)).status, { timeout: 15_000 })
        .toBe(200);
      return id;
    };
    try {
      // Remote development daemons publish loopback ports on their own host.
      // Use only an explicitly selected existing SSH transport; never run a shell.
      const sshHost = process.env.TEST_DOCKER_SSH_HOST;
      if (sshHost) {
        if (!/^[A-Za-z0-9][A-Za-z0-9_.@-]*$/.test(sshHost))
          throw new Error("Invalid explicit Docker SSH host");
        tunnel = spawn(
          "ssh",
          [
            "-N",
            "-o",
            "BatchMode=yes",
            "-o",
            "ExitOnForwardFailure=yes",
            "-L",
            `127.0.0.1:${port}:127.0.0.1:${port}`,
            sshHost,
          ],
          { stdio: "ignore" },
        );
        tunnelClosed = new Promise((done, fail) => {
          tunnel?.once("error", fail);
          tunnel?.once("close", () => done());
        });
      }
      if (postgresRestore) {
        docker("network", "create", network);
        networkCreated = true;
        const databaseVolume = `scratchpad-go-postgres-${suffix}`;
        docker("volume", "create", databaseVolume);
        volumes.push(databaseVolume);
        databaseContainer = docker(
          "run",
          "-d",
          "--network",
          network,
          "--network-alias",
          "database",
          "-v",
          `${databaseVolume}:/var/lib/postgresql`,
          "-e",
          "POSTGRES_USER=scratchpad",
          "-e",
          "POSTGRES_PASSWORD",
          "-e",
          "POSTGRES_DB=source",
          ragRestore
            ? "pgvector/pgvector:0.8.7-pg18-trixie@sha256:9d9c930220cb9bf2f956d10a8f909cf9973d9672ca0278aef2c1e6facccad2e0"
            : "postgres:18.6",
        );
        containers.push(databaseContainer);
        await expect
          .poll(
            () =>
              spawnSync(
                "docker",
                [
                  "--context",
                  context ?? "",
                  "exec",
                  databaseContainer,
                  "pg_isready",
                  "-h",
                  "127.0.0.1",
                  "-U",
                  "scratchpad",
                  "-d",
                  "source",
                ],
                { timeout: 10_000 },
              ).status,
            { timeout: 20_000 },
          )
          .toBe(0);
        databaseURL =
          "postgres://scratchpad:synthetic-container-database-password@database:5432/source?sslmode=disable";
        if (ragRestore)
          docker(
            "exec",
            databaseContainer,
            "psql",
            "-U",
            "scratchpad",
            "-d",
            "source",
            "-v",
            "ON_ERROR_STOP=1",
            "-c",
            "CREATE EXTENSION vector",
          );
      }
      if (secretRestore || ragRestore) {
        if (!networkCreated) {
          docker("network", "create", network);
          networkCreated = true;
        }
        for (const [volume, value] of [
          [correctKeyVolume, 44],
          [wrongKeyVolume, 45],
        ] as const) {
          if (!secretRestore) continue;
          docker("volume", "create", volume);
          volumes.push(volume);
          docker(
            "run",
            "--rm",
            "-v",
            `${volume}:/data`,
            "--entrypoint",
            "node",
            image ?? "",
            "-e",
            `require('node:fs').writeFileSync('/data/installation-key',Buffer.alloc(32,${value}).toString('base64')+'\\n',{mode:0o600})`,
          );
        }
        const provider = docker(
          "run",
          "-d",
          "--network",
          network,
          "--network-alias",
          "provider",
          "--entrypoint",
          "node",
          image ?? "",
          "-e",
          "let calls=0,anonymousCalls=0;require('node:http').createServer((req,res)=>{let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{res.setHeader('Content-Type','application/json');if(req.url==='/stats'){res.end(JSON.stringify({calls,anonymousCalls}));return}if(req.headers.authorization&&req.headers.authorization!=='Bearer synthetic-container-provider-token'){res.writeHead(401).end();return}calls++;if(!req.headers.authorization)anonymousCalls++;const body=JSON.parse(raw||'{}');const count=Array.isArray(body.input)?body.input.length:1;res.end(JSON.stringify(req.url==='/v1/embeddings'?{data:Array.from({length:count},(_,index)=>({index,embedding:[1,0]}))}:{choices:[{message:{content:'{\"ok\":true}'}}]}))})}).listen(8080,'0.0.0.0')",
        );
        containers.push(provider);
        providerContainer = provider;
      }
      docker("volume", "create", sourceVolume);
      volumes.push(sourceVolume);
      const sourceImage = rollback ? (retainedImage ?? "") : (image ?? "");
      const setupArgs = rollback
        ? [
            "--entrypoint",
            "node",
            sourceImage,
            "--import",
            "tsx",
            "/app/apps/web/src/server/admin.ts",
            "setup",
          ]
        : [sourceImage, "setup"];
      const token =
        docker(
          "run",
          "--rm",
          "-v",
          `${sourceVolume}:/data`,
          ...(postgresRestore
            ? ["--network", network, "-e", "SCRATCHPAD_DATABASE_URL"]
            : []),
          ...setupArgs,
        )
          .split("\n")
          .at(-1) ?? "";
      const source = await start(sourceVolume, sourceImage);
      const cdp = await page.context().newCDPSession(page);
      await cdp.send("WebAuthn.enable");
      await cdp.send("WebAuthn.addVirtualAuthenticator", {
        options: {
          protocol: "ctap2",
          transport: "internal",
          hasResidentKey: true,
          hasUserVerification: true,
          isUserVerified: true,
          automaticPresenceSimulation: true,
        },
      });
      await page.goto(origin);
      await page.getByLabel("Setup or recovery token").fill(token);
      await page
        .getByRole("button", { name: "Register passkey", exact: true })
        .click();
      await page.getByRole("button", { name: "Projects", exact: true }).click();
      await page
        .getByLabel("Project name", { exact: true })
        .fill("Container restore project");
      await page
        .getByRole("button", { name: "Create project", exact: true })
        .click();
      await expect(
        page.getByRole("heading", {
          name: "Container restore project",
          exact: true,
        }),
      ).toBeVisible();
      await page.getByRole("button", { name: "Memory", exact: true }).click();
      await page
        .getByRole("button", { name: "New record", exact: true })
        .click();
      await page.getByLabel("Type", { exact: true }).selectOption("finding");
      await page
        .getByLabel("Title", { exact: true })
        .fill("Container snapshot evidence");
      await page
        .getByLabel("Finding", { exact: true })
        .fill("The operational snapshot retains this immutable capture.");
      const captureResponse = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/records") &&
          response.request().method() === "POST",
      );
      await page
        .getByRole("button", { name: "Save record", exact: true })
        .click();
      const original = (await (await captureResponse).json()) as {
        record: { id: string; projectId: string };
      };
      if (ragRestore) {
        const project = await (
          await page.request.get(
            `${origin}/api/v1/projects/${encodeURIComponent(original.record.projectId)}`,
          )
        ).json();
        expect(
          (
            await page.request.patch(
              `${origin}/api/v1/projects/${encodeURIComponent(original.record.projectId)}/settings`,
              {
                headers: { Origin: origin },
                data: {
                  expectedVersion: project.project.version,
                  settings: {
                    ...project.project.settings,
                    aiProcessing: true,
                    crossProjectAnalysis: false,
                  },
                },
              },
            )
          ).status(),
        ).toBe(200);
        expect(
          (
            await page.request.patch(`${origin}/api/v1/ai/settings`, {
              headers: { Origin: origin },
              data: {
                expectedVersion: 0,
                enabled: true,
                scheduleMinutes: 0,
                baseUrl: "http://provider:8080/v1",
                model: "synthetic-completion",
                embeddingModel: "synthetic-embedding",
                embeddingDimensions: 2,
                requestTimeoutSeconds: 5,
              },
            })
          ).status(),
        ).toBe(200);
        await expect
          .poll(
            async () =>
              (
                await (
                  await page.request.get(
                    `${origin}/api/v1/rag/coverage?projectId=${encodeURIComponent(original.record.projectId)}`,
                  )
                ).json()
              ).status,
            { timeout: 30_000 },
          )
          .toBe("complete");
        const coverage = await (
          await page.request.get(
            `${origin}/api/v1/rag/coverage?projectId=${encodeURIComponent(original.record.projectId)}`,
          )
        ).json();
        expect(coverage.indexedSources).toBe(1);
        expect(coverage.passageCount).toBeGreaterThan(0);
        const evidence = await (
          await page.request.post(`${origin}/api/v1/rag/retrieve`, {
            headers: { Origin: origin },
            data: {
              projectId: original.record.projectId,
              query: "operational snapshot",
            },
          })
        ).json();
        expect(evidence.semantic).toMatchObject({
          available: true,
          indexCoverage: "complete",
        });
        sourcePassageId = evidence.results[0].id;
        expect(sourcePassageId).toMatch(/^passage_/);
      }
      if (secretRestore) {
        const configured = await page.request.patch(
          `${origin}/api/v1/ai/settings`,
          {
            headers: { Origin: origin },
            data: {
              expectedVersion: 0,
              enabled: true,
              baseUrl: "http://provider:8080/v1",
              model: "synthetic-completion",
              embeddingModel: "synthetic-embedding",
              embeddingDimensions: 2,
              requestTimeoutSeconds: 5,
              scheduleMinutes: 0,
              apiKey: "synthetic-container-provider-token",
            },
          },
        );
        expect(configured.status()).toBe(200);
        expect(
          (
            await page.request.post(`${origin}/api/v1/ai/test`, {
              headers: { Origin: origin },
              data: {},
            })
          ).status(),
        ).toBe(200);
      }
      await page
        .getByRole("button", { name: /Container snapshot evidence/ })
        .click();
      await expect(
        page.getByRole("heading", {
          name: "Container snapshot evidence",
          exact: true,
        }),
      ).toBeVisible();
      if (postgresRestore) {
        archive = execFileSync(
          "docker",
          [
            "--context",
            context ?? "",
            "exec",
            databaseContainer,
            "pg_dump",
            "-U",
            "scratchpad",
            "-d",
            "source",
            "--format=custom",
            "--compress=0",
            "--no-owner",
            "--no-acl",
          ],
          { timeout: 30_000, stdio: ["ignore", "pipe", "pipe"] },
        );
        expect(archive.subarray(0, 5).toString()).toBe("PGDMP");
      } else if (rollback) {
        expect(
          docker(
            "exec",
            source,
            "node",
            "--import",
            "tsx",
            "/app/apps/web/src/server/admin.ts",
            "backup",
            "/data/checkpoint.sqlite",
          ),
        ).toContain("Backup created:");
      } else
        expect(
          JSON.parse(
            docker(
              "exec",
              source,
              "/usr/local/bin/scratchpad-server",
              "backup",
              "/data/checkpoint.sqlite",
            ),
          ),
        ).toMatchObject({ database: "sqlite", schemaVersion: 3 });
      if (secretRestore) {
        if (postgresRestore)
          expect(
            archive?.includes(
              Buffer.from("synthetic-container-provider-token"),
            ),
          ).toBe(false);
        else
          docker(
            "exec",
            source,
            "node",
            "-e",
            "process.exit(require('node:fs').readFileSync('/data/checkpoint.sqlite').includes(Buffer.from('synthetic-container-provider-token'))?1:0)",
          );
      }
      await page.getByRole("button", { name: "Memory", exact: true }).click();
      await page
        .getByRole("button", { name: "New record", exact: true })
        .click();
      await page.getByLabel("Type", { exact: true }).selectOption("finding");
      await page
        .getByLabel("Title", { exact: true })
        .fill("After snapshot capture");
      await page
        .getByLabel("Finding", { exact: true })
        .fill("The restored snapshot must exclude this later write.");
      await page
        .getByRole("button", { name: "Save record", exact: true })
        .click();
      await expect(
        page.getByRole("button", { name: /After snapshot capture/ }),
      ).toBeVisible();
      stop(source, !rollback);
      if (ragRestore) sourceProviderCalls = providerCalls();
      if (rollback) {
        const upgraded = await start(sourceVolume);
        await page.goto(origin);
        await page
          .getByRole("button", { name: "Sign out", exact: true })
          .click();
        await page
          .getByRole("button", { name: "Sign in with passkey", exact: true })
          .click();
        await expect(
          page.getByRole("button", { name: /After snapshot capture/ }),
        ).toBeVisible();
        const migrated = await (
          await page.request.get(
            `${origin}/api/v1/records/${encodeURIComponent(original.record.id)}`,
          )
        ).json();
        expect(migrated.record).toEqual(original.record);
        await page
          .getByRole("button", { name: "New record", exact: true })
          .click();
        await page.getByLabel("Type", { exact: true }).selectOption("finding");
        await page
          .getByLabel("Title", { exact: true })
          .fill("Go post-upgrade capture");
        await page
          .getByLabel("Finding", { exact: true })
          .fill("Preserve this on the upgraded source only.");
        await page
          .getByRole("button", { name: "Save record", exact: true })
          .click();
        await expect(
          page.getByRole("button", { name: /Go post-upgrade capture/ }),
        ).toBeVisible();
        docker("stop", "--time", "20", upgraded);
        expect(
          docker("inspect", "--format", "{{.State.ExitCode}}", upgraded),
        ).toBe("0");
        const refused = run(sourceVolume, sourceImage);
        await expect
          .poll(
            async () => {
              try {
                return (await fetch(`${origin}/ready`)).status;
              } catch {
                return 0;
              }
            },
            { timeout: 15_000 },
          )
          .toBe(503);
        expect(
          (await page.request.get(`${origin}/api/v1/records`)).status(),
        ).toBe(503);
        stop(refused, false);
        const preserved = await start(sourceVolume);
        await page.goto(origin);
        await expect(
          page.getByRole("button", { name: /Go post-upgrade capture/ }),
        ).toBeVisible();
        docker("stop", "--time", "20", preserved);
        expect(
          docker("inspect", "--format", "{{.State.ExitCode}}", preserved),
        ).toBe("0");
      }
      docker("volume", "create", restoredVolume);
      volumes.push(restoredVolume);
      if (postgresRestore) {
        docker(
          "exec",
          databaseContainer,
          "createdb",
          "-U",
          "scratchpad",
          "restored",
        );
        execFileSync(
          "docker",
          [
            "--context",
            context ?? "",
            "exec",
            "-i",
            databaseContainer,
            "pg_restore",
            "-U",
            "scratchpad",
            "--dbname=restored",
            "--exit-on-error",
            "--no-owner",
            "--no-acl",
          ],
          { input: archive, timeout: 30_000, stdio: ["pipe", "pipe", "pipe"] },
        );
        databaseURL =
          "postgres://scratchpad:synthetic-container-database-password@database:5432/restored?sslmode=disable";
      } else
        docker(
          "run",
          "--rm",
          "-v",
          `${sourceVolume}:/source:ro`,
          "-v",
          `${restoredVolume}:/data`,
          "--entrypoint",
          "node",
          image ?? "",
          "-e",
          "const fs=require('node:fs');fs.copyFileSync('/source/checkpoint.sqlite','/data/scratchpad.sqlite');fs.chmodSync('/data/scratchpad.sqlite',0o600)",
        );
      const restored = await start(restoredVolume, sourceImage);
      await page.goto(origin);
      if (!rollback)
        await page
          .getByRole("button", { name: "Sign out", exact: true })
          .click();
      await page
        .getByRole("button", { name: "Sign in with passkey", exact: true })
        .click();
      await expect(
        page.getByRole("button", { name: /Container snapshot evidence/ }),
      ).toBeVisible();
      await expect(
        page.getByRole("button", { name: /After snapshot capture/ }),
      ).toHaveCount(0);
      await expect(
        page.getByRole("button", { name: /Go post-upgrade capture/ }),
      ).toHaveCount(0);
      await page
        .getByRole("button", { name: /Container snapshot evidence/ })
        .click();
      await expect(
        page.getByRole("heading", {
          name: "Container snapshot evidence",
          exact: true,
        }),
      ).toBeVisible();
      await expect(
        page
          .getByText(
            "The operational snapshot retains this immutable capture.",
            {
              exact: true,
            },
          )
          .first(),
      ).toBeVisible();
      const recovered = await (
        await page.request.get(
          `${origin}/api/v1/records/${encodeURIComponent(original.record.id)}`,
        )
      ).json();
      expect(recovered.record).toEqual(original.record);
      if (ragRestore) {
        expect(providerCalls()).toBe(sourceProviderCalls);
        const retrieved = await page.request.post(
          `${origin}/api/v1/rag/retrieve`,
          {
            headers: { Origin: origin },
            data: {
              projectId: original.record.projectId,
              query: "operational snapshot",
            },
          },
        );
        expect(retrieved.status()).toBe(200);
        const evidence = await retrieved.json();
        expect(evidence.semantic).toMatchObject({
          available: true,
          indexCoverage: "complete",
        });
        expect(evidence.results.length).toBeGreaterThan(0);
        expect(evidence.results[0]).toMatchObject({
          recordId: original.record.id,
          sourceUrl: `/api/v1/records/${original.record.id}`,
        });
        expect(evidence.results[0].id).toBe(sourcePassageId);
        expect(evidence.results[0].text).toContain(
          "The operational snapshot retains this immutable capture.",
        );
        docker("kill", providerContainer);
        expect(
          docker(
            "inspect",
            "--format",
            "{{.State.ExitCode}}",
            providerContainer,
          ),
        ).toBe("137");
        const fallback = await (
          await page.request.post(`${origin}/api/v1/rag/retrieve`, {
            headers: { Origin: origin },
            data: {
              projectId: original.record.projectId,
              query: "operational snapshot",
            },
          })
        ).json();
        expect(fallback.semantic).toMatchObject({
          available: false,
          reason: "AI_UNAVAILABLE",
        });
        expect(fallback.results[0]).toMatchObject({
          recordId: original.record.id,
          sourceUrl: `/api/v1/records/${original.record.id}`,
        });
        expect(fallback.results[0].text).toContain(
          "The operational snapshot retains this immutable capture.",
        );
      }
      if (secretRestore) {
        expect(
          (
            await page.request.post(`${origin}/api/v1/ai/test`, {
              headers: { Origin: origin },
              data: {},
            })
          ).status(),
        ).toBe(200);
      }
      stop(restored, !rollback);
      if (secretRestore) {
        for (const keyVolume of [wrongKeyVolume, ""]) {
          selectedKeyVolume = keyVolume;
          const unavailable = await start(restoredVolume);
          const denied = await page.request.post(`${origin}/api/v1/ai/test`, {
            headers: { Origin: origin },
            data: {},
          });
          expect(denied.status()).toBe(503);
          expect((await denied.json()).error.code).toBe("SECRET_UNAVAILABLE");
          const readable = await (
            await page.request.get(
              `${origin}/api/v1/records/${encodeURIComponent(original.record.id)}`,
            )
          ).json();
          expect(readable.record).toEqual(original.record);
          const capture = await page.request.post(`${origin}/api/v1/records`, {
            headers: { Origin: origin },
            data: {
              projectId: original.record.projectId,
              record: {
                type: "finding",
                title: `Memory with ${keyVolume ? "wrong" : "missing"} key`,
                authority: "explicit",
                confidence: "high",
                payload: {
                  finding:
                    "Capture remains available while provider secrets cannot be decrypted.",
                },
              },
            },
          });
          expect(capture.status()).toBe(201);
          expect((await capture.json()).record.payload.finding).toBe(
            "Capture remains available while provider secrets cannot be decrypted.",
          );
          expect(
            JSON.parse(
              docker(
                "exec",
                unavailable,
                "node",
                "-e",
                "fetch('http://provider:8080/stats').then(r=>r.text()).then(console.log)",
              ),
            ).calls,
          ).toBe(4);
          stop(unavailable, true);
        }
        selectedKeyVolume = correctKeyVolume;
        const retained = await start(restoredVolume);
        expect(
          (
            await page.request.post(`${origin}/api/v1/ai/test`, {
              headers: { Origin: origin },
              data: {},
            })
          ).status(),
        ).toBe(200);
        expect(
          JSON.parse(
            docker(
              "exec",
              retained,
              "node",
              "-e",
              "fetch('http://provider:8080/stats').then(r=>r.text()).then(console.log)",
            ),
          ),
        ).toEqual({ calls: 6, anonymousCalls: 0 });
        stop(retained, true);
        selectedKeyVolume = "";
        const secretless = await start(restoredVolume);
        const configuration = await (
          await page.request.get(`${origin}/api/v1/ai/settings`)
        ).json();
        const removed = await page.request.patch(
          `${origin}/api/v1/ai/settings`,
          {
            headers: { Origin: origin },
            data: { expectedVersion: configuration.version, apiKey: "" },
          },
        );
        expect(removed.status()).toBe(200);
        expect((await removed.json()).apiKeyConfigured).toBe(false);
        expect(
          (
            await page.request.post(`${origin}/api/v1/ai/test`, {
              headers: { Origin: origin },
              data: {},
            })
          ).status(),
        ).toBe(200);
        expect(
          JSON.parse(
            docker(
              "exec",
              secretless,
              "node",
              "-e",
              "fetch('http://provider:8080/stats').then(r=>r.text()).then(console.log)",
            ),
          ),
        ).toEqual({ calls: 8, anonymousCalls: 2 });
        stop(secretless, true);
      }
    } finally {
      try {
        for (const id of containers) docker("rm", "-f", "-v", id);
        for (const volume of volumes) docker("volume", "rm", volume);
        if (networkCreated) docker("network", "rm", network);
      } finally {
        tunnel?.kill("SIGTERM");
        await tunnelClosed;
      }
    }
  });
}
