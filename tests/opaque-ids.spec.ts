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

for (const maliciousId of ["victim/accept?", "../../auth/logout#"])
  test(`a displayed Reject for ${maliciousId} never sends Accept or logout`, async ({
    page,
  }) => {
    const writes: string[] = [];
    await page.route("**/api/v1/**", async (route) => {
      const request = route.request(),
        path = new URL(request.url()).pathname;
      if (request.method() !== "GET") writes.push(path);
      const body = path.endsWith("/auth/status")
        ? { initialized: true, authenticated: true, githubConfigured: false }
        : path.endsWith("/projects")
          ? { projects: [] }
          : path.endsWith("/records")
            ? { records: [], nextCursor: null }
            : path.endsWith("/suggestions")
              ? {
                  suggestions: [
                    {
                      id: maliciousId,
                      kind: "summary",
                      title: "Synthetic malformed suggestion",
                      content: { text: "Synthetic" },
                      sourceRecordIds: [],
                      status: "pending",
                      version: 1,
                      createdAt: new Date().toISOString(),
                    },
                  ],
                }
              : path.endsWith("/ai/jobs")
                ? { jobs: [] }
                : {};
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    });
    await page.goto(harness.origin);
    await page.getByRole("button", { name: "Insights", exact: true }).click();
    await expect(
      page.getByText("Synthetic malformed suggestion", { exact: true }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Reject suggestion", exact: true })
      .click();
    await expect(
      page.getByText("Invalid entity identifier.", { exact: true }),
    ).toBeVisible();
    expect(writes).toEqual([]);
    await expect(
      page.getByRole("button", { name: "Insights", exact: true }),
    ).toBeVisible();
  });
