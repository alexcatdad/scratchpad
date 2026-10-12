import { rmSync } from "node:fs";
import { expect, test } from "@playwright/test";
import { createWorkflowHarness } from "./workflow-harness";

const harness = createWorkflowHarness();
test.beforeAll(harness.start);
test.afterAll(async () => {
  await harness.stop();
  harness.cleanupDocker();
  rmSync(harness.temporary, { recursive: true, force: true });
});

test("legacy job responses still poll visible active work without page controls", async ({
  page,
}) => {
  let jobRequests = 0;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const body = path.endsWith("/auth/status")
      ? { initialized: true, authenticated: true, githubConfigured: false }
      : path.endsWith("/projects")
        ? { projects: [] }
        : path.endsWith("/records")
          ? { records: [], nextCursor: null }
          : path.endsWith("/suggestions")
            ? { suggestions: [] }
            : path.endsWith("/ai/jobs")
              ? {
                  jobs: [
                    {
                      id: "legacy_job",
                      type: "embed",
                      status: "queued",
                      version: 1,
                      attempts: 0,
                      createdAt: "2026-10-01T00:00:00Z",
                    },
                  ],
                }
              : {};
    if (path.endsWith("/ai/jobs")) jobRequests++;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(body),
    });
  });
  await page.goto(harness.origin);
  await page.getByRole("button", { name: "Insights", exact: true }).click();
  await expect(page.locator(".job-list")).toContainText("Queued");
  await expect(
    page.getByRole("button", { name: "Older jobs", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Latest jobs", exact: true }),
  ).toHaveCount(0);
  await expect.poll(() => jobRequests, { timeout: 6500 }).toBeGreaterThan(1);
});

test("job pages replace history and keep polling active work outside the page", async ({
  page,
}, testInfo) => {
  const requests: string[] = [];
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    let body: unknown = {};
    if (url.pathname.endsWith("/auth/status"))
      body = {
        initialized: true,
        authenticated: true,
        githubConfigured: false,
      };
    else if (url.pathname.endsWith("/projects")) body = { projects: [] };
    else if (url.pathname.endsWith("/records"))
      body = { records: [], nextCursor: null };
    else if (url.pathname.endsWith("/suggestions")) body = { suggestions: [] };
    else if (url.pathname.endsWith("/ai/jobs")) {
      const cursor = url.searchParams.get("cursor") ?? "";
      requests.push(cursor);
      body = {
        jobs: [
          {
            id: cursor ? "job_old" : "job_latest",
            type: cursor ? "embed" : "analyze",
            status: "completed",
            attempts: 1,
            version: 1,
            createdAt: "2026-10-01T00:00:00Z",
          },
        ],
        hasActive: true,
        ...(cursor ? {} : { nextCursor: "older-cursor" }),
      };
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(body),
    });
  });
  await page.goto(harness.origin);
  await page.getByRole("button", { name: "Insights", exact: true }).click();
  const jobs = page.locator(".job-list");
  await expect(jobs).toContainText("Analyze");
  await expect
    .poll(() => requests.filter((cursor) => cursor === "").length, {
      timeout: 6500,
    })
    .toBeGreaterThan(1);
  await page.getByRole("button", { name: "Older jobs", exact: true }).click();
  await expect(jobs).toContainText("Embed");
  await expect(jobs).not.toContainText("Analyze");
  await expect(
    page.getByRole("button", { name: "Older jobs", exact: true }),
  ).toHaveCount(0);
  await expect
    .poll(() => requests.filter((cursor) => cursor === "older-cursor").length, {
      timeout: 6500,
    })
    .toBeGreaterThan(1);
  await page.getByRole("button", { name: "Latest jobs", exact: true }).click();
  await expect(jobs).toContainText("Analyze");
  await expect(jobs).not.toContainText("Embed");
  await jobs.scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath("jobs-desktop.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  await jobs.scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath("jobs-mobile.png") });
  expect(errors).toEqual([]);
});
