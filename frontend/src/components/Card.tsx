import type { Card as CardModel, CardKind } from "../model";
import { cardKey, cardTitle, urgencyTier } from "../model";

export interface CardActions {
  onRun: (kind: CardKind, card: CardModel) => void;
  onJump: (kind: CardKind, card: CardModel) => void;
  onFlag: (kind: CardKind, card: CardModel) => void;
  onMenu: (kind: CardKind, card: CardModel, anchor: HTMLElement) => void;
}

const KIND_CLASS: Record<CardKind, string> = {
  task: "task-card",
  goal: "goal-card",
  topic: "topic-card",
};

// Card renders one board card. The class names are the e2e suite's contract —
// `.task-card`, `.goal-card`, `.topic-card`, `.task-title-link`, `.menu-btn`,
// `.start-btn`, `.jump-btn` — so they are not styling choices and must not be
// renamed without re-targeting the Go specs.
export default function Card({
  kind,
  card,
  actions,
  now,
}: {
  kind: CardKind;
  card: CardModel;
  actions: CardActions;
  now: Date;
}) {
  const tier = urgencyTier(
    (card as { due_date?: string }).due_date ?? (card as { planned_date?: string }).planned_date,
    now,
  );
  const sessionLive = (card as { session_state?: string }).session_state === "live";
  const flag = Boolean((card as { flag?: boolean }).flag);
  const assignee = (card as { assignee?: string }).assignee;
  const blocked = Boolean((card as { blocked?: boolean }).blocked);

  return (
    <article
      className={`${KIND_CLASS[kind]} urgency-${tier}`}
      data-card-key={cardKey(kind, card)}
      data-task-id={cardTitle(card)}
    >
      <header>
        <a
          className="task-title-link"
          href={(card as { obsidian_url: string }).obsidian_url}
          title={cardTitle(card)}
        >
          {cardTitle(card)}
        </a>
        <button
          type="button"
          className="menu-btn"
          aria-label="Card menu"
          onClick={(event) => actions.onMenu(kind, card, event.currentTarget)}
        >
          ⋮
        </button>
      </header>

      <div className="badges">
        {flag && <span className="badge flag-badge">★</span>}
        {assignee && <span className="badge assignee-badge">{assignee}</span>}
        {blocked && <span className="badge blocked-badge">blocked</span>}
        {sessionLive && <span className="badge live-badge">● live</span>}
      </div>

      <footer>
        {sessionLive ? (
          <button
            type="button"
            className="jump-btn"
            onClick={() => actions.onJump(kind, card)}
          >
            Jump
          </button>
        ) : (
          <button
            type="button"
            className="start-btn"
            onClick={() => actions.onRun(kind, card)}
          >
            Start
          </button>
        )}
        <button type="button" className="flag-btn" onClick={() => actions.onFlag(kind, card)}>
          {flag ? "Unflag" : "Flag"}
        </button>
      </footer>
    </article>
  );
}
