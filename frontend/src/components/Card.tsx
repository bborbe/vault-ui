import type { HoldResponse } from "../types";
import type { Card as CardModel, CardKind } from "../model";
import {
  CARD_CONFIG,
  cardKey,
  formatActivityAge,
  isTerminal,
  jiraBadge,
  modeLabel,
  modeTitle,
  pageUrlFromObsidianUrl,
  startingElapsed,
  titleParts,
  urgencyTier,
} from "../model";

export interface CardActions {
  onRun: (kind: CardKind, card: CardModel) => void;
  onJump: (kind: CardKind, card: CardModel) => void;
  onFlag: (kind: CardKind, card: CardModel) => void;
  onMenu: (kind: CardKind, card: CardModel, anchor: HTMLElement) => void;
  onAssign: (kind: CardKind, card: CardModel) => void;
  onAssigneeFilter: (assignee: string) => void;
  onDetail: (kind: CardKind, card: CardModel) => void;
  onBlocker: (kind: CardKind, name: string) => void;
  onTakeOver: (kind: CardKind, card: CardModel) => void;
}

// Card renders one board card. The class names are the e2e suite's contract —
// `.task-card`, `.goal-card`, `.topic-card`, `.task-title-link`, `.menu-btn`,
// `.start-btn`, `.jump-btn`, `.live-badge` — so they are not styling choices
// and must not be renamed without re-targeting the Go specs.
export default function Card({
  kind,
  card,
  actions,
  now,
  starting,
  activeAssignees,
  onDragStart,
  onDragEnd,
}: {
  kind: CardKind;
  card: CardModel;
  actions: CardActions;
  now: Date;
  starting: Set<string>;
  activeAssignees: string[];
  onDragStart?: (id: string) => void;
  onDragEnd?: () => void;
}) {
  const config = CARD_CONFIG[kind];
  const status = (card as { status?: string }).status;
  const flag = Boolean((card as { flag?: boolean }).flag);
  const assignee = (card as { assignee?: string }).assignee || "";
  const priority = (card as { priority?: unknown }).priority;
  const mode = (card as { mode?: string }).mode || "";
  const activityDate = (card as { activity_date?: string }).activity_date;
  const blockers = ((card as { blockers?: string[] }).blockers ?? []) as string[];
  const upcoming = Boolean((card as { upcoming?: boolean }).upcoming);
  const recentlyCompleted = Boolean((card as { recently_completed?: boolean }).recently_completed);
  const hold = status === "hold";
  const obsidianUrl = (card as { obsidian_url?: string }).obsidian_url || "";

  const jira = jiraBadge(
    (card as { title: string }).title,
    (card as { jira?: string }).jira,
  );
  const pageUrl = pageUrlFromObsidianUrl(obsidianUrl);
  const { head, tail } = titleParts(jira.title);

  const tier = kind === "task" ? urgencyTier(card as { due_date?: string; planned_date?: string }, now) : null;
  const classes = [
    "task-card",
    config.extraClass,
    tier === 0 ? "urgency-overdue" : tier === 1 ? "urgency-today" : tier === 2 ? "urgency-scheduled" : "",
    upcoming ? "upcoming" : "",
    recentlyCompleted || (config.greyWhenTerminal && isTerminal(status)) ? "recently-completed" : "",
    hold ? "on-hold" : "",
    flag ? "flagged" : "",
  ]
    .filter(Boolean)
    .join(" ");

  const dataAttrs: Record<string, string> = {
    [`data-${config.idDataset.replace(/[A-Z]/g, (m) => `-${m.toLowerCase()}`)}`]: cardKey(kind, card),
  };
  if (config.kindDataset !== null) dataAttrs["data-kind"] = config.kindDataset;
  if (config.vaultDataset) dataAttrs["data-vault"] = (card as { vault: string }).vault;

  const activityAge = formatActivityAge(activityDate, now);

  return (
    <div
      className={classes}
      {...dataAttrs}
      draggable={config.draggable}
      onDragStart={
        config.draggable
          ? (event) => {
              event.dataTransfer.effectAllowed = "move";
              event.dataTransfer.setData("text/plain", (card as { id: string }).id);
              event.currentTarget.classList.add("dragging");
              onDragStart?.((card as { id: string }).id);
            }
          : undefined
      }
      onDragEnd={
        config.draggable
          ? () => {
              document.querySelectorAll(".task-card.dragging").forEach((el) => el.classList.remove("dragging"));
              onDragEnd?.();
            }
          : undefined
      }
    >
      {config.menu && (
        <button
          type="button"
          className="menu-btn"
          aria-label="Card menu"
          onClick={(event) => actions.onMenu(kind, card, event.currentTarget)}
        >
          ⋮
        </button>
      )}

      <div className="card-content">
        <h3 className="task-title">
          <a
            href={pageUrl || obsidianUrl}
            className={`task-title-link${config.titleLinkExtraClass ? ` ${config.titleLinkExtraClass}` : ""}`}
            title={pageUrl ? "Open page" : "Open in Obsidian"}
          >
            {head}
            <span className="title-tail">
              {tail}
              {!pageUrl && <span className="obsidian-icon">↗</span>}
            </span>
          </a>
          {pageUrl && (
            <a
              href={obsidianUrl}
              className="obsidian-icon obsidian-handoff"
              title="Open in Obsidian"
              aria-label="Open in Obsidian"
            >
              ↗
            </a>
          )}
        </h3>
      </div>

      {blockers.length > 0 && (
        <div className="card-blocked">
          <span
            className="blocked-badge clickable"
            title={`blocked by ${blockers.join(", ")}`}
            onClick={() => actions.onBlocker(kind, blockers[0])}
          >
            {`blocked by ${blockers.join(", ")}`}
          </span>
        </div>
      )}

      <HoldReasonRow card={card} flag={flag} status={status} />

      {jira.issueKey && jira.issueUrl && (
        <div className="card-jira">
          <a href={jira.issueUrl} className="jira-badge" target="_blank" rel="noreferrer" title="Open in Jira">
            <span className="jira-icon">🔖</span>
            <span>{jira.issueKey}</span>
          </a>
        </div>
      )}

      <div className="card-footer">
        <div className="card-footer-left">
          <button
            type="button"
            className={`flag-btn${flag ? " flagged" : ""}`}
            onClick={() => actions.onFlag(kind, card)}
            title={flag ? "Unflag — not picked for today" : "Flag — picked for today"}
          >
            {flag ? "🚩" : "⚑"}
          </button>
          {hold && (
            <span className="hold-badge" title={config.holdTitle}>
              ⏸ HOLD
            </span>
          )}
          {assignee ? (
            config.assigneeClickable ? (
              <span
                className={`assignee-badge clickable${activeAssignees.includes(assignee) ? " active" : ""}`}
                onClick={() => actions.onAssigneeFilter(assignee)}
                title={activeAssignees.includes(assignee) ? "Clear filter" : `Filter by ${assignee}`}
              >
                <span className="assignee-icon">👤</span>
                <span>{assignee}</span>
              </span>
            ) : (
              <span className="assignee-badge">
                <span className="assignee-icon">👤</span>
                <span>{assignee}</span>
              </span>
            )
          ) : kind !== "topic" ? (
            <a
              className="assign-to-me-link"
              onClick={() => actions.onAssign(kind, card)}
              title={`Assign this ${kind} to me`}
            >
              + Assign to me
            </a>
          ) : null}
          {priority !== null && priority !== undefined && priority !== "" && (
            <span className="priority-chip" title={`Priority ${String(priority)}`}>
              P{String(priority)}
            </span>
          )}
          {mode && (
            <span className="mode-badge" title={modeTitle(mode)}>
              {modeLabel(mode)}
            </span>
          )}
          {config.detail && (
            <button
              type="button"
              className="topic-detail-btn"
              title="Show tracked work"
              aria-label="Show tracked work"
              onClick={() => actions.onDetail(kind, card)}
            >
              📋
            </button>
          )}
        </div>
        <div className="card-actions">
          {activityAge && (
            <span className="activity-age" title={`Last activity: ${activityDate}`}>
              {activityAge}
            </span>
          )}
          <SessionControl kind={kind} card={card} now={now} starting={starting} actions={actions} />
        </div>
      </div>
    </div>
  );
}

// SessionControl is the one Start / Resume / Starting / Live control shared by
// all three card kinds. A terminal card renders no control at all.
function SessionControl({
  kind,
  card,
  now,
  starting,
  actions,
}: {
  kind: CardKind;
  card: CardModel;
  now: Date;
  starting: Set<string>;
  actions: CardActions;
}) {
  const status = (card as { status?: string }).status;
  const id = (card as { id: string }).id;
  const sessionID = (card as { claude_session_id?: string }).claude_session_id;
  const startedMarker = (card as { claude_session_started?: string }).claude_session_started;
  const sessionState = (card as { session_state?: string }).session_state;
  const location = (card as { location?: string }).location;
  const jumpPane = (card as { jump_pane?: string }).jump_pane;

  if (isTerminal(status)) {
    return (
      <span className="done-chip" title="Finished — no session control on a closed card">
        ✓ Done
      </span>
    );
  }

  // Marker means "launch turn in flight": Starting wins even once the id lands,
  // so a reload mid-launch stays "Starting…" instead of offering a take-over on
  // a booting session.
  if (startedMarker || starting.has(id)) {
    const elapsed = startingElapsed(startedMarker, now);
    return (
      <span
        className="starting-badge"
        role="button"
        tabIndex={0}
        title={`Launch turn in flight${elapsed ? ` (${elapsed.trim()})` : ""} — click to take over and resume (ends the running launch turn; in-flight work is lost).`}
        onClick={() => actions.onTakeOver(kind, card)}
      >
        ⏳ Starting...{elapsed}
      </span>
    );
  }

  if (sessionState === "live") {
    return (
      <>
        <span
          className="live-badge"
          role="button"
          tabIndex={0}
          title="Session is live — click to take over and resume (ends the running turn; in-flight work is lost)"
          onClick={() => actions.onTakeOver(kind, card)}
        >
          {location ? `● Live·${location}` : "● Live"}
        </span>
        {jumpPane && (
          <span
            className="jump-btn"
            role="button"
            tabIndex={0}
            title="Jump to this session's terminal pane"
            onClick={() => actions.onJump(kind, card)}
          >
            ↗
          </span>
        )}
      </>
    );
  }

  if (sessionID) {
    const indeterminate = sessionState === "indeterminate";
    return (
      <button
        type="button"
        className={`resume-btn${indeterminate ? " indeterminate" : ""}`}
        disabled={indeterminate}
        title={indeterminate ? "Session state unknown — may be open elsewhere; resume disabled" : undefined}
        onClick={() => actions.onRun(kind, card)}
      >
        ▶ Resume
      </button>
    );
  }

  return (
    <button type="button" className="start-btn" onClick={() => actions.onRun(kind, card)}>
      ▶ Start
    </button>
  );
}

// HoldReasonRow renders why claude-wrapper's flagged tick holds a card rather
// than opening it. A reason the card already says is suppressed.
function HoldReasonRow({
  card,
  flag,
  status,
}: {
  card: CardModel;
  flag: boolean;
  status?: string;
}) {
  const hold = (card as { hold_reason?: HoldResponse }).hold_reason;
  if (!hold || !flag || isTerminal(status)) return null;
  if (hold.reason === "not-absent" && (card as { session_state?: string }).session_state === "live") {
    return null;
  }
  const blockers = hold.blockers ?? [];
  if (hold.reason === "blocked" && blockers.length > 0) return null;

  const code = String(hold.reason || "").replace(/[^a-z0-9-]/g, "");
  const lines: string[] = [];
  if (hold.summary) lines.push(hold.summary);
  if (hold.until) lines.push(`until ${hold.until}`);
  if (hold.since) lines.push(`since ${hold.since}`);
  if (hold.card_id) lines.push(`card ${hold.card_id}`);
  if (hold.pr_url) lines.push(hold.pr_url);
  if (blockers.length) lines.push(`blocked by ${blockers.join(", ")}`);
  lines.push(`held by claude-wrapper: ${hold.reason || "unknown"}`);

  return (
    <div className="card-hold-reason">
      <span className={`hold-reason hold-reason-${code}`} title={lines.join("\n")}>
        <span className="hold-reason-label">{hold.label || "held"}</span>
        {hold.summary && <span className="hold-reason-summary"> {hold.summary}</span>}
        {safeHttpUrl(hold.pr_url) && (
          <a
            className="hold-reason-pr"
            href={safeHttpUrl(hold.pr_url)}
            target="_blank"
            rel="noreferrer"
            onClick={(event) => event.stopPropagation()}
          >
            {" "}PR
          </a>
        )}
      </span>
    </div>
  );
}

function safeHttpUrl(value?: string): string {
  if (!value) return "";
  try {
    const parsed = new URL(value);
    if (parsed.protocol === "https:" || parsed.protocol === "http:") return parsed.href;
  } catch {
    return "";
  }
  return "";
}
