import { useState } from "react";
import type { Card as CardModel, CardKind, SortKey } from "../model";
import { PHASE_LABELS, UNKNOWN_COLUMN, bucketCards, columnsFor } from "../model";
import Card, { type CardActions } from "./Card";

// Board renders the columns and their cards. The `data-phase` attribute, the
// `.kanban-column` / `.cards` structure and the `.view-toggle-btn[data-view]`
// buttons are the e2e suite's contract and the old stylesheet's selectors.
export default function Board({
  kind,
  cards,
  sort,
  actions,
  now,
  starting,
  activeAssignees,
  onDropPhase,
}: {
  kind: CardKind;
  cards: CardModel[];
  sort: SortKey;
  actions: CardActions;
  now: Date;
  starting: Set<string>;
  activeAssignees: string[];
  onDropPhase: (taskID: string, phase: string) => void;
}) {
  const [dragOver, setDragOver] = useState<string | null>(null);
  const { byColumn } = bucketCards(kind, cards, sort);
  const columns = columnsFor(kind);

  return (
    <div className="kanban-board">
      {columns.map((column) => {
        const columnCards = byColumn.get(column) ?? [];
        // The "—" lane exists only for goals and topics that carry no phase,
        // and stays hidden while it holds nothing.
        const hidden = column === UNKNOWN_COLUMN && columnCards.length === 0;
        return (
          <div
            key={column}
            className={`kanban-column${hidden ? " hidden" : ""}`}
            data-phase={column}
          >
            <h2 data-column-header={column}>{PHASE_LABELS[column] ?? column}</h2>
            <div
              className={`cards${dragOver === column ? " drag-over" : ""}`}
              id={`cards-${column}`}
              onDragOver={(event) => {
                event.preventDefault();
                setDragOver(column);
              }}
              onDragLeave={() => setDragOver((current) => (current === column ? null : current))}
              onDrop={(event) => {
                event.preventDefault();
                setDragOver(null);
                const taskID = event.dataTransfer.getData("text/plain");
                // Only a card this board is showing may move: a stale or
                // foreign drag must not PATCH an id the user cannot see.
                if (!taskID) return;
                if (!cards.some((card) => (card as { id: string }).id === taskID)) return;
                onDropPhase(taskID, column);
              }}
            >
              {columnCards.map((card) => (
                <Card
                  key={`${kind}:${(card as { vault?: string }).vault ?? ""}:${(card as { id: string }).id}`}
                  kind={kind}
                  card={card}
                  actions={actions}
                  now={now}
                  starting={starting}
                  activeAssignees={activeAssignees}
                />
              ))}
            </div>
          </div>
        );
      })}
    </div>
  );
}
