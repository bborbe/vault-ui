import { describe, expect, it } from "vitest";
import { UNKNOWN_COLUMN, columnFor, priorityValue, sortCards, urgencyTier } from "./model";

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
});

describe("priorityValue", () => {
  it("reads numbers, numeric strings and labels", () => {
    expect(priorityValue(2)).toBe(2);
    expect(priorityValue("3")).toBe(3);
    expect(priorityValue("high")).toBe(1);
    expect(priorityValue(undefined)).toBe(Number.MAX_SAFE_INTEGER);
  });
});

describe("sortCards", () => {
  it("puts flagged cards first in every sort mode", () => {
    const cards = [
      { id: "a", flag: false, priority: 1 },
      { id: "b", flag: true, priority: 9 },
    ];
    expect(sortCards(cards, "priority").map((c) => c.id)).toEqual(["b", "a"]);
    expect(sortCards(cards, "default").map((c) => c.id)).toEqual(["b", "a"]);
  });

  it("orders by priority when asked", () => {
    const cards = [
      { id: "a", flag: false, priority: 3 },
      { id: "b", flag: false, priority: 1 },
    ];
    expect(sortCards(cards, "priority").map((c) => c.id)).toEqual(["b", "a"]);
  });

  it("does not mutate the input slice", () => {
    const cards = [
      { id: "a", flag: false, priority: 3 },
      { id: "b", flag: false, priority: 1 },
    ];
    sortCards(cards, "priority");
    expect(cards.map((c) => c.id)).toEqual(["a", "b"]);
  });
});

describe("urgencyTier", () => {
  const now = new Date(2026, 10, 27);

  it("buckets overdue, today, this week and later", () => {
    expect(urgencyTier("2026-11-26", now)).toBe(0);
    expect(urgencyTier("2026-11-27", now)).toBe(1);
    expect(urgencyTier("2026-11-30", now)).toBe(2);
    expect(urgencyTier("2027-01-01", now)).toBe(3);
  });

  it("treats an absent or unparseable date as no urgency", () => {
    expect(urgencyTier(undefined, now)).toBe(3);
    expect(urgencyTier("not-a-date", now)).toBe(3);
  });
});
