// The board's view model. Pure functions only: no DOM, no fetch. The column
// vocabulary, the per-kind fallbacks, the sort profiles and the card slots are
// ported from the old app.js so the e2e suite's DOM contract keeps meaning the
// same thing.
import type { GoalResponse, TaskResponse, TopicResponse } from "./types";

export type CardKind = "task" | "goal" | "topic";
export type View = "tasks" | "goals" | "topics";
export type Card = TaskResponse | GoalResponse | TopicResponse;

// The closed status enum, in display order.
export const ALL_STATUSES = [
  "next",
  "in_progress",
  "backlog",
  "completed",
  "hold",
  "aborted",
] as const;

export const PHASE_COLUMNS = [
  "todo",
  "planning",
  "execution",
  "ai_review",
  "human_review",
  "done",
] as const;

// The column a card with no recognised phase falls into. Tasks fall back to
// todo (actionable and unstarted); goals and topics fall back to the "—"
// column, because the board never writes their phase itself.
export const UNKNOWN_COLUMN = "unknown";

export const PHASE_LABELS: Record<string, string> = {
  todo: "Todo",
  planning: "Planning",
  in_progress: "Execution",
  execution: "Execution",
  ai_review: "AI Review",
  human_review: "Human Review",
  done: "Done",
  unknown: "—",
};

// defaultStatuses is the kind-aware default applied only on first load, when
// the URL carries no `status` at all. A view switch never rewrites it: the
// filter belongs to the operator, not to the view.
export function defaultStatuses(view: View): string[] {
  if (view === "goals" || view === "topics") {
    return ["backlog", "next", "in_progress", "hold", "completed"];
  }
  return ["in_progress", "hold", "completed"];
}

// columnsFor returns the column order for a view. The unknown column is only
// rendered for goals and topics; the tasks view never shows it.
export function columnsFor(kind: CardKind): string[] {
  return kind === "task"
    ? [...PHASE_COLUMNS]
    : [...PHASE_COLUMNS, UNKNOWN_COLUMN];
}

// columnFor maps a card to its column. `in_progress` is the legacy spelling of
// `execution` and renders in that column; any other unrecognised value takes
// the kind's fallback rather than disappearing.
export function columnFor(kind: CardKind, phase?: string | null): string {
  const resolved = phase === "in_progress" ? "execution" : phase;
  const fallback = kind === "task" ? "todo" : UNKNOWN_COLUMN;
  if (resolved && (PHASE_COLUMNS as readonly string[]).includes(resolved)) {
    return resolved;
  }
  return fallback;
}

// isTerminal is the one predicate for "this work is finished". A terminal card
// offers no session control at all.
export function isTerminal(status?: string | null): boolean {
  return status === "completed" || status === "aborted";
}

// priorityValue normalises the polymorphic priority field. The API emits it as
// a number, a numeric string or a label; the board sorts on a number.
export function priorityValue(priority: unknown): number {
  if (typeof priority === "number") return priority;
  if (typeof priority === "string") {
    const parsed = Number.parseInt(priority, 10);
    if (!Number.isNaN(parsed)) return parsed;
    switch (priority.toLowerCase()) {
      case "high":
      case "highest":
        return 1;
      case "medium":
        return 2;
      case "low":
        return 3;
      default:
        return 999;
    }
  }
  return 999;
}

export type SortKey = "default" | "priority" | "modified";

// urgencyTier buckets a task against today, using the same rules the old board
// did: 0 = overdue, 1 = due today, 2 = planned for today or earlier,
// 3 = nothing applicable. It drives the card's border class.
export function urgencyTier(
  card: { due_date?: string; planned_date?: string },
  now: Date,
): number {
  const today = now.toISOString().slice(0, 10);
  const valid = (value?: string) =>
    value && /^\d{4}-\d{2}-\d{2}$/.test(value) ? value : null;
  const due = valid(card.due_date);
  const planned = valid(card.planned_date);
  if (due && due < today) return 0;
  if (due && due === today) return 1;
  if (planned && planned <= today) return 2;
  return 3;
}

// activityDateMs is the epoch of a card's activity_date, or -Infinity so a
// missing date sinks under the descending "modified" order.
function activityDateMs(card: Card): number {
  const value = (card as { activity_date?: string }).activity_date;
  return value ? new Date(value).getTime() : -Infinity;
}

// defaultSortCompare is each view's pre-sort-control ordering, preserved
// verbatim: tasks by urgency then priority; goals and topics by priority then
// id for a stable tiebreak.
function defaultSortCompare(a: Card, b: Card, kind: CardKind): number {
  if (kind === "task") {
    const ta = urgencyTier(a as TaskResponse, new Date());
    const tb = urgencyTier(b as TaskResponse, new Date());
    if (ta !== tb) return ta - tb;
  }
  const pa = priorityValue(a.priority);
  const pb = priorityValue(b.priority);
  if (pa !== pb) return pa - pb;
  if (kind !== "task") {
    return (a.id || "").localeCompare(b.id || "");
  }
  return 0;
}

// sortCards orders a column. Flagged cards come first in every mode — the flag
// is the operator's own "look at this" marker and beats urgency, priority and
// recency.
export function sortCards(cards: Card[], key: SortKey, kind: CardKind): Card[] {
  const sorted = [...cards];
  sorted.sort((a, b) => {
    const flagged = Number(Boolean(b.flag)) - Number(Boolean(a.flag));
    if (flagged !== 0) return flagged;
    if (key === "priority") {
      return priorityValue(a.priority) - priorityValue(b.priority);
    }
    if (key === "modified") {
      const ma = activityDateMs(a);
      const mb = activityDateMs(b);
      if (ma !== mb) return mb - ma;
    }
    return defaultSortCompare(a, b, kind);
  });
  return sorted;
}

// matchesAssignee mirrors the server's `assignee` filter for kinds whose
// endpoint has no such parameter (topics). An empty selection matches
// everything; "" is the control's "unassigned" token.
export function matchesAssignee(assignees: string[], card: Card): boolean {
  if (assignees.length === 0) return true;
  return assignees.includes((card as { assignee?: string }).assignee || "");
}

// matchesStatus is the client-side status filter for the topics endpoint, which
// returns every topic by design.
export function matchesStatus(statuses: string[], card: Card): boolean {
  return statuses.includes((card as { status?: string }).status || "in_progress");
}

export interface BucketedCards {
  byColumn: Map<string, Card[]>;
  pinned: Card[];
}

// bucketCards places every card in its column. Order within a column is
// active → upcoming → recently completed → hold: a parked item sinks below
// everything else. A held card is never placed in Done at all — a parked task
// is work awaiting a start, not finished work — so a held card whose phase is
// `done` falls back to Todo.
export function bucketCards(kind: CardKind, cards: Card[], sort: SortKey): BucketedCards {
  const byColumn = new Map<string, Card[]>();
  for (const column of columnsFor(kind)) byColumn.set(column, []);

  const pinned: Card[] = [];
  const active: Card[] = [];
  const upcoming: Card[] = [];
  const hold: Card[] = [];

  for (const card of cards) {
    if (Boolean((card as TaskResponse).recently_completed)) {
      pinned.push(card);
      continue;
    }
    const status = (card as { status?: string }).status;
    if (status === "hold") {
      hold.push(card);
    } else if (Boolean((card as { upcoming?: boolean }).upcoming)) {
      upcoming.push(card);
    } else {
      active.push(card);
    }
  }

  const place = (card: Card, column: string) => {
    const bucket = byColumn.get(column);
    (bucket ?? byColumn.get(UNKNOWN_COLUMN)!).push(card);
  };

  const push = (cards: Card[]) => {
    for (const card of cards) place(card, columnFor(kind, (card as { phase?: string }).phase));
  };

  // Sort each group on its own, then distribute in group order: a column must
  // read active → upcoming, and a per-column re-sort would undo that.
  push([...sortCards(active, sort, kind), ...sortCards(upcoming, sort, kind)]);

  // Recently completed go to Done above anything held: a card that just
  // finished outranks a parked one.
  byColumn.set("done", [...byColumn.get("done")!, ...sortCards(pinned, sort, kind)]);

  // Held cards are the last group in every column, and never land in Done: a
  // held card whose phase is `done` falls back to Todo. Flag-first ordering
  // still applies inside the held group.
  for (const card of sortCards(hold, sort, kind)) {
    const column = columnFor(kind, (card as { phase?: string }).phase);
    place(card, column === "done" ? "todo" : column);
  }
  return { byColumn, pinned };
}

// cardKey is the identity the board uses. Topics key on the vault-qualified
// pair because a topic name is a filename and is not unique across vaults.
export function cardKey(kind: CardKind, card: Card): string {
  if (kind === "topic") {
    return `${(card as TopicResponse).vault}\u0000${(card as TopicResponse).id}`;
  }
  return (card as TaskResponse).id;
}

// itemKey is cardKey for an item named only by kind, vault and id — the shape a
// write_failed event carries — so it addresses the same pending entry.
export function itemKey(kind: CardKind, vault: string, id: string): string {
  return cardKey(kind, { id, vault } as unknown as Card);
}

export function cardTitle(card: Card): string {
  return (card as TaskResponse).title;
}

// extractJira strips a Jira issue key out of a heading and resolves the key's
// project to its Atlassian domain. The key is also returned so a card can badge
// it; an unknown project keeps the key but yields no URL.
export interface JiraRef {
  title: string;
  issueKey: string;
  issueUrl: string;
}

const JIRA_KEY = /\b([A-Z]+)-(\d+)\b/;
const JIRA_DOMAINS: Record<string, string> = {
  BRO: "seibertgroup.atlassian.net",
  TRADE: "borbe.atlassian.net",
};

export function extractJira(text: string): JiraRef {
  const match = text.match(JIRA_KEY);
  if (!match) return { title: text, issueKey: "", issueUrl: "" };
  const issueKey = match[0];
  const domain = JIRA_DOMAINS[match[1]];
  return {
    title: text.replace(JIRA_KEY, "").trim(),
    issueKey,
    issueUrl: domain ? `https://${domain}/browse/${issueKey}` : "",
  };
}

// jiraBadge resolves a card's Jira badge from the page's own `jira:` value,
// falling back to the key in the title. The heading always comes from the
// title, because extractJira strips the key out of the text it returns.
export function jiraBadge(
  title: string,
  jira?: string | null,
): { title: string; issueKey: string; issueUrl: string } {
  const heading = extractJira(title).title;
  const source = typeof jira === "string" && jira.trim() !== "" ? jira : title;
  const ref = extractJira(source);
  return { title: heading, issueKey: ref.issueKey, issueUrl: ref.issueUrl };
}

// pageUrlFromObsidianUrl turns the board's obsidian:// deep link into the
// in-app page route, so a card opens the page inside vault-ui instead of
// handing it to Obsidian. "" when the link carries no vault or file.
export function pageUrlFromObsidianUrl(obsidianUrl?: string | null): string {
  if (!obsidianUrl) return "";
  try {
    const parsed = new URL(obsidianUrl);
    const vault = parsed.searchParams.get("vault") || "";
    const file = parsed.searchParams.get("file") || "";
    if (!vault || !file) return "";
    const encodedPath = file.split("/").map(encodeURIComponent).join("/");
    return `/v/${encodeURIComponent(vault)}/page/${encodedPath}`;
  } catch {
    return "";
  }
}

// formatActivityAge renders the age of a card's last activity as one largest
// unit — glanceable staleness, not precision.
export function formatActivityAge(iso: string | null | undefined, now: Date): string {
  if (!iso) return "";
  const then = new Date(iso);
  if (Number.isNaN(then.getTime())) return "";
  const seconds = Math.floor((now.getTime() - then.getTime()) / 1000);
  if (seconds < 60) return "<1m";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days}d`;
  return `${Math.floor(days / 7)}w`;
}

// startingElapsed is the "Starting…" badge's elapsed clock, from the durable
// launch marker. A legacy marker is the literal "true" and carries no age.
export function startingElapsed(marker: string | null | undefined, now: Date): string {
  if (!marker || marker === "true") return "";
  const started = Date.parse(marker);
  if (Number.isNaN(started)) return "";
  const secs = Math.max(0, Math.floor((now.getTime() - started) / 1000));
  const mins = Math.floor(secs / 60);
  return ` ${mins}:${String(secs % 60).padStart(2, "0")}`;
}

// modeLabel is the execution-mode pill's text. The field is free-form on disk,
// so the two known modes get a friendly label and any other value renders
// verbatim.
export function modeLabel(mode: string): string {
  if (mode === "headless") return "⚙ headless";
  if (mode === "interactive") return "🖥 interactive";
  return mode;
}

export function modeTitle(mode: string): string {
  if (mode === "headless") return "Starts a headless worker (no UI)";
  if (mode === "interactive") return "Opens an interactive session";
  return "Execution mode";
}

// titleParts splits a heading so the Obsidian arrow can be glued to its last
// word: a title that filled its last line used to push a lone "↗" onto a line
// of its own.
export function titleParts(title: string): { head: string; tail: string } {
  const cut = title.lastIndexOf(" ");
  if (cut === -1) return { head: "", tail: title };
  return { head: title.slice(0, cut + 1), tail: title.slice(cut + 1) };
}

export function kindLabel(kind: CardKind): string {
  return kind === "goal" ? "Goal" : kind === "topic" ? "Topic" : "Task";
}

export function routeFor(kind: CardKind): "tasks" | "goals" | "topics" {
  return kind === "goal" ? "goals" : kind === "topic" ? "topics" : "tasks";
}

// CardConfig describes which affordances a card kind carries. The card
// template branches on these fields, never on the kind name.
export interface CardConfig {
  draggable: boolean;
  menu: boolean;
  detail: boolean;
  assigneeClickable: boolean;
  greyWhenTerminal: boolean;
  idDataset: "taskId" | "goalId" | "topicId";
  kindDataset: string | null;
  vaultDataset: boolean;
  extraClass: string;
  titleLinkExtraClass: string;
  holdTitle: string;
}

export const CARD_CONFIG: Record<CardKind, CardConfig> = {
  task: {
    draggable: true,
    menu: true,
    detail: false,
    assigneeClickable: true,
    greyWhenTerminal: false,
    idDataset: "taskId",
    kindDataset: null,
    vaultDataset: false,
    extraClass: "",
    titleLinkExtraClass: "",
    holdTitle: "On hold — blocked, not actively worked",
  },
  goal: {
    draggable: false,
    menu: true,
    detail: false,
    assigneeClickable: true,
    greyWhenTerminal: true,
    idDataset: "goalId",
    kindDataset: "goal",
    vaultDataset: false,
    extraClass: "goal-card",
    titleLinkExtraClass: "",
    holdTitle: "On hold — paused, not actively worked",
  },
  topic: {
    draggable: false,
    menu: false,
    detail: true,
    assigneeClickable: false,
    greyWhenTerminal: true,
    idDataset: "topicId",
    kindDataset: null,
    vaultDataset: true,
    extraClass: "topic-card",
    titleLinkExtraClass: "topic-title-link",
    holdTitle: "On hold — paused, not actively worked",
  },
};
