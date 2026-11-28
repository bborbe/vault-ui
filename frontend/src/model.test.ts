import { describe, expect, it } from "vitest";
import type { Card } from "./model";
import {
  UNKNOWN_COLUMN,
  bucketCards,
  cardKey,
  columnFor,
  extractJira,
  itemKey,
  matchesAssignee,
  pageUrlFromObsidianUrl,
  priorityValue,
  sortCards,
  urgencyTier,
} from "./model";
import { buildSearch, parseSearch } from "./state";

describe("columnFor", () => {
  it("falls back to todo for a task with no phase", () => {
    expect(columnFor("task", undefined)).toBe("todo");
    expect(columnFor("task", null)).toBe("todo");
  });

  it("renames the legacy in_progress phase into the execution column", () => {
    expect(columnFor("task", "in_progress")).toBe("execution");
    expect(columnFor("task", "execution")).toBe("execution");
  });

  it("sends a phase-less goal or topic to the unknown column, never todo", () => {
    expect(columnFor("goal", undefined)).toBe(UNKNOWN_COLUMN);
    expect(columnFor("topic", null)).toBe(UNKNOWN_COLUMN);
    expect(columnFor("goal", "planning")).toBe("planning");
  });

  it("sends an unrecognised phase to the kind's fallback", () => {
    expect(columnFor("task", "nonsense")).toBe("todo");
    expect(columnFor("topic", "nonsense")).toBe(UNKNOWN_COLUMN);
  });
});

describe("priorityValue", () => {
  it("reads numbers, numeric strings and labels", () => {
    expect(priorityValue(2)).toBe(2);
    expect(priorityValue("3")).toBe(3);
    expect(priorityValue("high")).toBe(1);
    expect(priorityValue(undefined)).toBe(999);
  });
});

describe("sortCards", () => {
  it("puts flagged cards first in every sort mode", () => {
    const cards = [
      { id: "a", flag: false, priority: 1 },
      { id: "b", flag: true, priority: 9 },
    ] as unknown as Card[];
    expect(sortCards(cards, "priority", "task").map((c) => c.id)).toEqual(["b", "a"]);
    expect(sortCards(cards, "default", "task").map((c) => c.id)).toEqual(["b", "a"]);
  });

  it("orders by priority when asked", () => {
    const cards = [
      { id: "a", flag: false, priority: 3 },
      { id: "b", flag: false, priority: 1 },
    ] as unknown as Card[];
    expect(sortCards(cards, "priority", "task").map((c) => c.id)).toEqual(["b", "a"]);
  });

  it("does not mutate the input slice", () => {
    const cards = [
      { id: "a", flag: false, priority: 3 },
      { id: "b", flag: false, priority: 1 },
    ] as unknown as Card[];
    sortCards(cards, "priority", "task");
    expect(cards.map((c) => c.id)).toEqual(["a", "b"]);
  });
});

describe("urgencyTier", () => {
  const now = new Date("2026-11-27T12:00:00Z");

  it("buckets overdue, today, this week and later", () => {
    expect(urgencyTier({ due_date: "2026-11-26" }, now)).toBe(0);
    expect(urgencyTier({ due_date: "2026-11-27" }, now)).toBe(1);
    expect(urgencyTier({ planned_date: "2026-11-30" }, now)).toBe(3);
    expect(urgencyTier({ planned_date: "2026-11-27" }, now)).toBe(2);
    expect(urgencyTier({ due_date: "2027-01-01" }, now)).toBe(3);
  });

  it("treats an absent or unparseable date as no urgency", () => {
    expect(urgencyTier({}, now)).toBe(3);
    expect(urgencyTier({ due_date: "not-a-date" }, now)).toBe(3);
  });
});

describe("extractJira", () => {
  it("strips a known project's key from the heading and resolves its domain", () => {
    expect(extractJira("BRO-123 Fix the thing")).toEqual({
      title: "Fix the thing",
      issueKey: "BRO-123",
      issueUrl: "https://seibertgroup.atlassian.net/browse/BRO-123",
    });
  });

  it("keeps the key but yields no URL for an unknown project", () => {
    expect(extractJira("XYZ-7 Do it")).toEqual({
      title: "Do it",
      issueKey: "XYZ-7",
      issueUrl: "",
    });
  });

  it("leaves a heading with no key untouched", () => {
    expect(extractJira("Plain title").title).toBe("Plain title");
  });
});

describe("pageUrlFromObsidianUrl", () => {
  it("converts an obsidian deep link into the in-app page route", () => {
    expect(
      pageUrlFromObsidianUrl(
        "obsidian://open?vault=fixture&file=25%20Tasks%2FCard%20Page%20Fixture.md",
      ),
    ).toBe("/v/fixture/page/25%20Tasks/Card%20Page%20Fixture.md");
  });

  it("returns empty for a link with no vault or file", () => {
    expect(pageUrlFromObsidianUrl("obsidian://open?vault=fixture")).toBe("");
    expect(pageUrlFromObsidianUrl("")).toBe("");
  });
});

describe("matchesAssignee", () => {
  it("matches everything when no filter is set", () => {
    expect(matchesAssignee([], { id: "a", title: "a" } as never)).toBe(true);
  });

  it("treats a missing assignee as the unassigned token", () => {
    expect(matchesAssignee([""], { id: "a", title: "a" } as never)).toBe(true);
    expect(matchesAssignee(["e2e"], { id: "a", title: "a" } as never)).toBe(false);
  });
});

describe("bucketCards", () => {
  it("pins recently completed tasks to the bottom of done", () => {
    const { byColumn } = bucketCards(
      "task",
      [
        { id: "active", title: "a", phase: "todo", flag: false, priority: 1 } as never,
        { id: "done", title: "d", phase: "done", flag: false, priority: 1, recently_completed: true } as never,
      ],
      "default",
    );
    expect(byColumn.get("todo")!.map((c) => c.id)).toEqual(["active"]);
    expect(byColumn.get("done")!.map((c) => c.id)).toEqual(["done"]);
  });

  it("sinks held and upcoming cards below the active ones", () => {
    const { byColumn } = bucketCards(
      "task",
      [
        { id: "hold", title: "h", phase: "todo", flag: false, priority: 1, status: "hold" } as never,
        { id: "upcoming", title: "u", phase: "todo", flag: false, priority: 1, upcoming: true } as never,
        { id: "active", title: "a", phase: "todo", flag: false, priority: 9 } as never,
      ],
      "default",
    );
    expect(byColumn.get("todo")!.map((c) => c.id)).toEqual(["active", "upcoming", "hold"]);
  });
});

describe("parseSearch / buildSearch", () => {
  it("applies the kind-aware status default when the URL carries none", () => {
    expect(parseSearch("?view=tasks", 8).statuses).toEqual(["in_progress", "hold", "completed"]);
    expect(parseSearch("?view=goals", 8).statuses).toEqual([
      "backlog",
      "next",
      "in_progress",
      "hold",
      "completed",
    ]);
  });

  it("round-trips the filter set through the query string", () => {
    const state = parseSearch(
      "?view=goals&assignee=e2e&status=hold&vault=fixture&sort=priority&session_live=true",
      8,
    );
    const search = buildSearch(state);
    expect(search).toContain("view=goals");
    expect(search).toContain("assignee=e2e");
    expect(search).toContain("status=hold");
    expect(search).toContain("vault=fixture");
    expect(search).toContain("sort=priority");
    expect(search).toContain("session_live=true");
  });

  it("always emits the view and the sort", () => {
    expect(buildSearch(parseSearch("", 8))).toContain("view=tasks");
    expect(buildSearch(parseSearch("", 8))).toContain("sort=default");
  });
});

describe("itemKey", () => {
  it("addresses the same entry cardKey does, for every kind", () => {
    const card = { id: "Task A", vault: "private-personal" } as unknown as Card;
    expect(itemKey("task", "private-personal", "Task A")).toBe(cardKey("task", card));
    expect(itemKey("goal", "private-personal", "Task A")).toBe(cardKey("goal", card));
    expect(itemKey("topic", "private-personal", "Task A")).toBe(cardKey("topic", card));
  });

  it("keeps topics vault-qualified", () => {
    expect(itemKey("topic", "a", "T")).not.toBe(itemKey("topic", "b", "T"));
  });
});
