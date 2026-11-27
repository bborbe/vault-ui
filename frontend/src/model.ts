// The board's view model. Pure functions only: no DOM, no fetch. The column
// vocabulary and the per-kind fallbacks are ported from the old app.js so the
// e2e suite's DOM contract keeps meaning the same thing.
import type { GoalResponse, TaskResponse, TopicResponse } from "./types";

export type CardKind = "task" | "goal" | "topic";
export type Card = TaskResponse | GoalResponse | TopicResponse;

// The closed status enum, in display order.
export const STATUSES = ["next", "in_progress", "backlog", "completed", "hold", "aborted"] as const;

// The phases a card can sit in. `unknown` is the column for a card that reports
// no phase at all, and it is hidden whenever it holds nothing.
export const UNKNOWN_COLUMN = "unknown";

export const PHASE_LABELS: Record<string, string> = {
  todo: "Todo",
  planning: "Planning",
  execution: "Execution",
  in_progress: "Execution",
  ai_review: "AI Review",
  human_review: "Human Review",
  done: "Done",
  unknown: "—",
};

// columnsFor returns the column order for a view. The unknown column is always
// last: it only ever shows cards that report no phase.
export function columnsFor(): string[] {
  return ["todo", "planning", "execution", "ai_review", "human_review", "done", UNKNOWN_COLUMN];
}

// columnFor maps a card to its column. A task with no phase falls back to todo
// (it is actionable and unstarted); a goal or topic with no phase falls back to
// the unknown column, because the board never writes their phase itself.
export function columnFor(kind: CardKind, phase?: string | null): string {
  if (kind !== "task") {
    return phase === undefined || phase === null || phase === "" ? UNKNOWN_COLUMN : phase;
  }
  if (phase === undefined || phase === null || phase === "") {
    return "todo";
  }
  // `in_progress` and `execution` are the same column; the legacy spelling is
  // renamed rather than given a column of its own.
  return phase === "in_progress" ? "execution" : phase;
}

// priorityValue normalises the polymorphic priority field. The API emits it as
// any of a number, a numeric string, or a label, and the board sorts on a
// number.
export function priorityValue(priority: unknown): number {
  if (typeof priority === "number") return priority;
  if (typeof priority === "string") {
    const parsed = Number.parseInt(priority, 10);
    if (!Number.isNaN(parsed)) return parsed;
    switch (priority.toLowerCase()) {
      case "high":
        return 1;
      case "medium":
        return 2;
      case "low":
        return 3;
      default:
        return Number.MAX_SAFE_INTEGER;
    }
  }
  return Number.MAX_SAFE_INTEGER;
}

export type SortKey = "default" | "priority" | "modified";

// sortCards orders a column. Flagged cards come first in every mode — the flag
// is the operator's own "look at this" marker.
export function sortCards<T extends { flag?: boolean }>(cards: T[], key: SortKey): T[] {
  const sorted = [...cards];
  sorted.sort((a, b) => {
    const flagged = Number(Boolean(b.flag)) - Number(Boolean(a.flag));
    if (flagged !== 0) return flagged;
    if (key === "priority") {
      return priorityValue((a as { priority?: unknown }).priority) -
        priorityValue((b as { priority?: unknown }).priority);
    }
    if (key === "modified") {
      const at = (a as { modified_date?: string | null }).modified_date ?? "";
      const bt = (b as { modified_date?: string | null }).modified_date ?? "";
      // Newest first; an absent date sorts last.
      return bt.localeCompare(at);
    }
    return 0;
  });
  return sorted;
}

// urgencyTier buckets a due or planned date against today: 0 = overdue,
// 1 = due today, 2 = within the week, 3 = later or none. It drives the card's
// border class, so it is part of the visible contract.
export function urgencyTier(dueDate: string | null | undefined, now: Date): number {
  if (!dueDate) return 3;
  const due = new Date(dueDate);
  if (Number.isNaN(due.getTime())) return 3;
  const dayMs = 24 * 60 * 60 * 1000;
  const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const days = Math.round((startOfDay(due) - startOfDay(now)) / dayMs);
  if (days < 0) return 0;
  if (days === 0) return 1;
  if (days <= 7) return 2;
  return 3;
}

// cardKey reads the one identifier the board uses. Topics key on the
// vault-qualified pair because a topic name is a filename and is not unique
// across vaults.
export function cardKey(kind: CardKind, card: Card): string {
  if (kind === "topic") {
    return `${(card as TopicResponse).vault}\u0000${(card as TopicResponse).id}`;
  }
  return (card as TaskResponse).id;
}

export function cardTitle(card: Card): string {
  return (card as TaskResponse).title;
}
