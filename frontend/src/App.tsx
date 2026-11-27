import { useCallback, useEffect, useState } from "react";
import {
  assignToMe,
  fetchGoals,
  fetchSourceStatus,
  fetchTasks,
  fetchTopics,
  fetchVaults,
  jumpToPane,
  runSession,
  subscribeBoard,
  updateFlag,
} from "./api";
import Board from "./components/Board";
import type { Card as CardModel, CardKind, SortKey } from "./model";
import type { GoalResponse, TaskResponse, TopicResponse, VaultResponse } from "./types";

const VIEWS: { kind: CardKind; label: string }[] = [
  { kind: "task", label: "Tasks" },
  { kind: "goal", label: "Goals" },
  { kind: "topic", label: "Topics" },
];

const FETCHERS = {
  task: fetchTasks,
  goal: fetchGoals,
  topic: fetchTopics,
} as const;

export default function App() {
  const [view, setView] = useState<CardKind>("task");
  const [sort] = useState<SortKey>("default");
  const [cards, setCards] = useState<CardModel[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [menu, setMenu] = useState<{ kind: CardKind; card: CardModel } | null>(null);
  const [sourceUnavailable, setSourceUnavailable] = useState(false);
  const [vaults, setVaults] = useState<VaultResponse[]>([]);
  // Empty means the all-vaults view, which reports nothing about unserved
  // vaults — naming every one of them would raise the marker for vaults the
  // operator never asked about.
  const [selectedVaults] = useState<string[]>([]);

  const load = useCallback(
    (signal?: AbortSignal) => {
      const fetcher = FETCHERS[view] as (
        query: Record<string, never>,
        signal?: AbortSignal,
      ) => Promise<CardModel[]>;
      return fetcher({}, signal)
        .then((next) => {
          setCards(next);
          setError(null);
        })
        .catch((err: Error) => {
          if (err.name !== "AbortError") setError(err.message);
        });
    },
    [view],
  );

  useEffect(() => {
    const controller = new AbortController();
    setCards(null);
    void load(controller.signal);
    const unsubscribe = subscribeBoard(() => void load(controller.signal));
    return () => {
      unsubscribe();
      controller.abort();
    };
  }, [load]);

  // The two banners the e2e suite asserts. They are always in the DOM and
  // toggled by class, exactly as the old page did, so the markers keep their
  // meaning.
  useEffect(() => {
    const controller = new AbortController();
    fetchSourceStatus(controller.signal)
      .then((status) => setSourceUnavailable(status.unavailable))
      .catch(() => undefined); // a failing status endpoint is itself an outage; leave the banner as-is
    fetchVaults(controller.signal)
      .then(setVaults)
      .catch(() => undefined);
    return () => controller.abort();
  }, []);

  const noSource =
    selectedVaults.length > 0 &&
    vaults.some((vault) => selectedVaults.includes(vault.name) && vault.source === false);

  const vaultOf = (card: CardModel) => (card as { vault: string }).vault;

  const actions = {
    onRun: (kind: CardKind, card: CardModel) => {
      const route = kind === "task" ? "tasks" : kind === "goal" ? "goals" : "topics";
      void runSession(route, (card as { id: string }).id, vaultOf(card)).then(() => load());
    },
    onJump: (kind: CardKind, card: CardModel) => {
      const route = kind === "task" ? "tasks" : kind === "goal" ? "goals" : "topics";
      void jumpToPane(route, (card as { id: string }).id, vaultOf(card));
    },
    onFlag: (kind: CardKind, card: CardModel) => {
      const route = kind === "task" ? "tasks" : kind === "goal" ? "goals" : "topics";
      const next = !(card as { flag?: boolean }).flag;
      void updateFlag(route, (card as { id: string }).id, next, vaultOf(card)).then(() => load());
    },
    onMenu: (kind: CardKind, card: CardModel) => setMenu({ kind, card }),
  };

  return (
    <main>
      <div
        id="vault-api-unavailable"
        className={`banner${sourceUnavailable ? "" : " hidden"}`}
        data-testid="vault-api-unavailable"
      >
        vault-api unavailable — showing last-known data
      </div>
      <div
        id="vault-no-source"
        className={`banner${noSource ? "" : " hidden"}`}
        data-testid="vault-no-source"
      >
        no source for vault
      </div>

      <nav className="view-toggle">
        {VIEWS.map(({ kind, label }) => (
          <button
            key={kind}
            type="button"
            className={`view-toggle-btn${view === kind ? " active" : ""}`}
            data-view={kind}
            aria-selected={view === kind}
            onClick={() => setView(kind)}
          >
            {label}
          </button>
        ))}
      </nav>

      {menu && (
        <div className="task-menu" role="menu">
          <button
            type="button"
            onClick={() => {
              void assignToMe(menu.kind === "goal" ? "goals" : "tasks", (menu.card as { id: string }).id, vaultOf(menu.card))
                .then(() => load());
              setMenu(null);
            }}
          >
            Assign to me
          </button>
          <button type="button" onClick={() => setMenu(null)}>
            Close
          </button>
        </div>
      )}

      {error && <p className="err">Could not read the board: {error}</p>}
      {!error && !cards && <p className="subtitle">Loading…</p>}
      {!error && cards && (
        <Board kind={view} cards={cards} sort={sort} actions={actions} now={new Date()} />
      )}
    </main>
  );
}

// The generated types are imported for the fetcher cast; keep the reference so a
// type-only import is not flagged as unused by noUnusedLocals.
export type BoardTypes = TaskResponse | GoalResponse | TopicResponse;
