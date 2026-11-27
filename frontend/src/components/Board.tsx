import type { Card as CardModel, CardKind } from "../model";
import { PHASE_LABELS, UNKNOWN_COLUMN, columnFor, columnsFor, sortCards, type SortKey } from "../model";
import Card, { type CardActions } from "./Card";

// Board renders the columns and their cards. The `data-phase` attribute and the
// `.view-toggle-btn[data-view]` buttons are the e2e suite's contract.
export default function Board({
  kind,
  cards,
  sort,
  actions,
  now,
}: {
  kind: CardKind;
  cards: CardModel[];
  sort: SortKey;
  actions: CardActions;
  now: Date;
}) {
  const columns = columnsFor();
  const byColumn = new Map<string, CardModel[]>();
  for (const column of columns) byColumn.set(column, []);
  for (const card of cards) {
    const column = columnFor(kind, (card as { phase?: string }).phase);
    const bucket = byColumn.get(column);
    // A card whose phase is outside the known set still has to be visible, so
    // it lands in the unknown column rather than disappearing.
    (bucket ?? byColumn.get(UNKNOWN_COLUMN)!).push(card);
  }

  return (
    <div className="board">
      {columns.map((column) => {
        const columnCards = sortCards(byColumn.get(column) ?? [], sort);
        // The unknown column is hidden whenever it holds nothing.
        const hidden = column === UNKNOWN_COLUMN && columnCards.length === 0;
        return (
          <section
            key={column}
            className={`kanban-column${hidden ? " hidden" : ""}`}
            data-phase={column}
          >
            <h2>{PHASE_LABELS[column] ?? column}</h2>
            {columnCards.map((card) => (
              <Card
                key={`${kind}:${(card as { vault?: string }).vault ?? ""}:${(card as { id: string }).id}`}
                kind={kind}
                card={card}
                actions={actions}
                now={now}
              />
            ))}
          </section>
        );
      })}
    </div>
  );
}
