import { expect, it } from "vitest";
import { projectContext } from "./context";
import { Store } from "./store";

it("orders context state by instant and uses receipt time for equal event instants", async () => {
  const store = new Store(":memory:");
  try {
    const project = await store.insert("project", {
      id: "project",
      name: "Dates",
    });
    const states = [
      {
        id: "z-state-second",
        happenedAt: "2026-01-01T00:00:00Z",
        createdAt: "2026-01-01T00:00:10Z",
        state: "paused",
      },
      {
        id: "a-state-sub-millisecond",
        happenedAt: "2026-01-01T00:00:00.0001Z",
        createdAt: "2026-01-01T00:00:10Z",
        state: "paused",
      },
      {
        id: "state-fraction",
        happenedAt: "2026-01-01T00:00:00.050Z",
        createdAt: "2026-01-01T00:00:10Z",
        state: "active",
      },
      {
        id: "state-equal-older",
        happenedAt: "2026-01-01T00:00:00.5Z",
        createdAt: "2026-01-01T00:00:10Z",
        state: "paused",
      },
      {
        id: "state-equal-newer",
        happenedAt: "2026-01-01T00:00:00.500Z",
        createdAt: "2026-01-01T00:00:10.050Z",
        state: "active",
      },
    ];
    for (const { state, ...dates } of states)
      await store.insert("record", {
        ...dates,
        projectId: project.id,
        type: "project_state",
        payload: { state },
      });
    const context = await projectContext(store, project);
    expect(context.currentState).toMatchObject({
      id: "state-equal-newer",
      payload: { state: "active" },
    });
    for (const field of ["state", "stateHistory", "applicableRecords"])
      expect(context[field]).toEqual(
        [
          "state-equal-newer",
          "state-equal-older",
          "state-fraction",
          "a-state-sub-millisecond",
          "z-state-second",
        ].map((id) => expect.objectContaining({ id })),
      );
  } finally {
    await store.close();
  }
});
