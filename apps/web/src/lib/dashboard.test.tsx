import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RecordContent } from "../components/record-content";
import { dashboardSearch, recordParams } from "./dashboard";

describe("dashboard boundaries", () => {
  it("round-trips record links and filters without trusting malformed URL values", () => {
    const parsed = dashboardSearch({
      view: "Settings",
      section: "data",
      recordId: "rec_example",
      q: "why sqlite",
      projectId: "proj_example",
      from: "2026-10-01",
      to: "not-a-date",
      type: "invalid",
      authority: ["explicit"],
    });
    expect(parsed).toEqual({
      view: "Settings",
      section: "data",
      recordId: "rec_example",
      q: "why sqlite",
      projectId: "proj_example",
      from: "2026-10-01",
    });
    const params = recordParams(parsed);
    expect(params.get("from")).toBe("2026-10-01T00:00:00.000Z");
    expect(params.get("q")).toBe("why sqlite");
    expect(params.has("recordId")).toBe(false);
    expect(params.has("view")).toBe(false);
  });
  it("renders structured source content without HTML, executable links or remote image requests", () => {
    const markup = renderToStaticMarkup(
      <RecordContent
        value={{
          finding:
            "## A heading\n\n- A list item\n\n[Evidence](https://example.com/proof)\n\n[Bad](javascript:alert(1))\n\n![Tracker](https://example.com/pixel.png)\n\n<script>alert(1)</script>",
          limitations: ["First", "Second"],
        }}
      />,
    );
    expect(markup).toContain("<h3>A heading</h3>");
    expect(markup).toContain("<li>A list item</li>");
    expect(markup).toContain('href="https://example.com/proof"');
    expect(markup).not.toContain("javascript:");
    expect(markup).not.toContain("<script");
    expect(markup).not.toContain("<img");
    expect(markup).not.toContain("pixel.png");
    expect(markup).toContain("First");
    expect(markup).toContain("Second");
  });
});
