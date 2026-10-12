import { execFileSync, spawnSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { createServer } from "node:net";
import { expect, test } from "@playwright/test";

for (const scenario of ["go-restore", "typescript-rollback"] as const) {
  test(`actual images preserve browser access through ${scenario}`, async ({
    page,
  }) => {
    const context = process.env.TEST_DOCKER_CONTEXT;
    const image = process.env.TEST_GO_IMAGE;
    const retainedImage = process.env.TEST_RETAINED_IMAGE;
    test.skip(
      !context ||
        !image ||
        (scenario === "typescript-rollback" && !retainedImage),
      "Explicit disposable Docker context and Go image are required",
    );
    const docker = (...args: string[]) =>
      execFileSync("docker", ["--context", context ?? "", ...args], {
        encoding: "utf8",
        timeout: 30_000,
        stdio: ["ignore", "pipe", "pipe"],
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
      docker("volume", "create", sourceVolume);
      volumes.push(sourceVolume);
      const sourceImage =
        scenario === "typescript-rollback"
          ? (retainedImage ?? "")
          : (image ?? "");
      const setupArgs =
        scenario === "typescript-rollback"
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
        docker("run", "--rm", "-v", `${sourceVolume}:/data`, ...setupArgs)
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
        record: { id: string };
      };
      await page
        .getByRole("button", { name: /Container snapshot evidence/ })
        .click();
      await expect(
        page.getByRole("heading", {
          name: "Container snapshot evidence",
          exact: true,
        }),
      ).toBeVisible();
      if (scenario === "typescript-rollback") {
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
        ).toMatchObject({ database: "sqlite", schemaVersion: 2 });
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
      stop(source, scenario === "go-restore");
      if (scenario === "typescript-rollback") {
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
      if (scenario === "go-restore")
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
      stop(restored, scenario === "go-restore");
    } finally {
      for (const id of containers) docker("rm", "-f", id);
      for (const volume of volumes) docker("volume", "rm", volume);
    }
  });
}
