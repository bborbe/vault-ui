import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import * as api from "./api";
import Board from "./components/Board";
import Menu, { type MenuAction, type MenuState } from "./components/Menu";
import {
  ApproveModal,
  type ApproveResult,
  type Dialog,
  LoadingModal,
  ReasonModal,
  type ReasonResult,
  SessionModal,
  TakeOverModal,
  TopicModal,
  Toasts,
  type ToastMessage,
} from "./components/Dialogs";
import type { CardActions } from "./components/Card";
import Toolbar from "./components/Toolbar";
import type { Card as CardModel, CardKind, SortKey } from "./model";
import { cardKey, matchesAssignee, matchesStatus, routeFor } from "./model";
import type { BoardState } from "./state";
import {
  buildSearch,
  parseSearch,
  persistVaults,
  readUpcoming,
  restoreVaults,
} from "./state";
import type {
  GoalResponse,
  SessionResponse,
  TaskResponse,
  TopicResponse,
  VaultResponse,
} from "./types";

// The phases the tasks endpoint is asked for: every column the board renders,
// including both spellings of the execution phase.
const TASK_PHASES = [
  "todo",
  "planning",
  "in_progress",
  "execution",
  "ai_review",
  "human_review",
  "done",
];

const VIEW_KIND: Record<BoardState["view"], CardKind> = {
  tasks: "task",
  goals: "goal",
  topics: "topic",
};

function storage(): Storage | undefined {
  return typeof window === "undefined" ? undefined : window.localStorage;
}

function buildQuery(state: BoardState): api.BoardQuery {
  const query: api.BoardQuery = {
    vault: state.vaults,
    upcomingHours: state.upcomingHours,
    sessionLive: state.liveOnly,
  };
  // The topics endpoint returns every topic by design and has no status or
  // assignee parameter, so both filters are applied client-side for it.
  if (state.view !== "topics") {
    query.status = state.statuses;
    query.assignee = state.assignees;
  }
  if (state.view === "tasks") {
    query.phase = TASK_PHASES;
    query.goal = state.goals;
  }
  return query;
}

export default function App() {
  const [state, setState] = useState<BoardState>(() =>
    parseSearch(window.location.search, readUpcoming(storage())),
  );
  const [vaults, setVaults] = useState<VaultResponse[]>([]);
  const [assigneeOptions, setAssigneeOptions] = useState<{
    named: string[];
    hasUnassigned: boolean;
  }>({ named: [], hasUnassigned: false });
  const [cards, setCards] = useState<CardModel[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sourceUnavailable, setSourceUnavailable] = useState(false);
  const [connected, setConnected] = useState(false);
  const [menu, setMenu] = useState<MenuState | null>(null);
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [session, setSession] = useState<SessionResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [topic, setTopic] = useState<{
    topic: TopicResponse;
    detail: Awaited<ReturnType<typeof api.fetchTopicDetail>> | null;
    error: string | null;
  } | null>(null);
  const [toasts, setToasts] = useState<ToastMessage[]>([]);
  const [starting, setStarting] = useState<Set<string>>(new Set());
  const [now, setNow] = useState(() => new Date());

  const pending = useRef(new Map<string, { fields: Record<string, unknown>; until: number }>());
  // Captured before any effect rewrites the address bar, so the persisted
  // vault selection is still restored on a bare load.
  const initialSearch = useRef(window.location.search);
  const toastID = useRef(0);
  const dialogResolve = useRef<((value: unknown) => void) | null>(null);
  const restored = useRef(false);

  const kind = VIEW_KIND[state.view];

  const addToast = useCallback((message: string, isError = false) => {
    const id = ++toastID.current;
    setToasts((current) => [...current, { id, message, error: isError }]);
    window.setTimeout(
      () => setToasts((current) => current.filter((toast) => toast.id !== id)),
      isError ? 4000 : 2000,
    );
  }, []);

  // The clock behind the activity-age labels. Thirty seconds is enough for a
  // glanceable age and cheap enough to leave running.
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 30000);
    return () => window.clearInterval(timer);
  }, []);

  // mergePending re-applies every optimistic write onto a freshly fetched list.
  // An entry the server has caught up with is dropped; an expired one is
  // dropped so a write that never lands cannot stick forever.
  const mergePending = useCallback((viewKind: CardKind, list: CardModel[]) => {
    const merged = list.map((card) => ({ ...card }));
    pending.current.forEach((entry, key) => {
      if (entry.until < Date.now()) {
        pending.current.delete(key);
        return;
      }
      const card = merged.find((candidate) => cardKey(viewKind, candidate) === key);
      if (!card) return;
      const caughtUp = Object.entries(entry.fields).every(
        ([field, value]) => (card as unknown as Record<string, unknown>)[field] === value,
      );
      if (caughtUp) {
        pending.current.delete(key);
        return;
      }
      Object.assign(card, entry.fields);
    });
    return merged;
  }, []);

  // loadView fetches only the active view's endpoint, so a deep link to
  // ?view=goals never touches /api/tasks.
  const loadView = useCallback(
    async (signal?: AbortSignal) => {
      const viewKind = VIEW_KIND[state.view];
      try {
        const query = buildQuery(state);
        const list =
          state.view === "goals"
            ? await api.fetchGoals(query, signal)
            : state.view === "topics"
              ? await api.fetchTopics(query, signal)
              : await api.fetchTasks(query, signal);
        const filtered =
          state.view === "topics"
            ? list.filter(
                (card) =>
                  matchesStatus(state.statuses, card as TopicResponse) &&
                  matchesAssignee(state.assignees, card as TopicResponse),
              )
            : list;
        setCards(mergePending(viewKind, filtered as CardModel[]));
        setError(null);
      } catch (err) {
        if ((err as Error).name !== "AbortError") setError((err as Error).message);
      }
      try {
        const status = await api.fetchSourceStatus(signal);
        setSourceUnavailable(status.unavailable);
      } catch {
        // A failing status endpoint is itself an outage; leave the banner as-is.
      }
    },
    // The filter fields are the fetch's inputs; `sort` is deliberately absent —
    // a sort change re-renders from the cards already held.
    [state.view, state.vaults, state.statuses, state.assignees, state.goals, state.liveOnly, state.upcomingHours, mergePending],
  );

  const loadRef = useRef(loadView);
  useEffect(() => {
    loadRef.current = loadView;
  }, [loadView]);

  const refreshVaults = useCallback(async () => {
    const list = await api.fetchVaults();
    setVaults(list);
    if (!restored.current && initialSearch.current === "") {
      restored.current = true;
      const restoredVaults = restoreVaults(
        storage(),
        list.map((vault) => vault.name),
      );
      if (restoredVaults.length > 0) {
        setState((current) =>
          current.vaults.length === 0 ? { ...current, vaults: restoredVaults } : current,
        );
      }
    }
  }, []);

  // Bootstrap: vaults and the source-status banner. Vaults gate the persisted
  // selection, so they come first.
  useEffect(() => {
    refreshVaults().catch((err: Error) => setError(err.message));
  }, [refreshVaults]);

  // Assignee options follow the selected vaults.
  useEffect(() => {
    const controller = new AbortController();
    api
      .fetchAssignees(state.vaults, controller.signal)
      .then((options) =>
        setAssigneeOptions({
          named: options.named ?? [],
          hasUnassigned: Boolean(options.has_unassigned),
        }),
      )
      .catch(() => undefined);
    return () => controller.abort();
  }, [state.vaults]);

  // The active view is re-read whenever a filter that reaches the server
  // changes.
  useEffect(() => {
    const controller = new AbortController();
    void loadView(controller.signal);
    return () => controller.abort();
  }, [loadView]);

  // Live updates: every event is a wake-up, so one code path builds the model.
  useEffect(() => {
    return api.subscribeBoard(
      () => {
        void loadRef.current();
      },
      setConnected,
    );
  }, []);

  // Keep the address bar and the persisted controls in step with the state.
  useEffect(() => {
    const search = buildSearch(state);
    window.history.replaceState({}, "", search === "" ? window.location.pathname : search);
    persistVaults(storage(), state.vaults);
    const store = storage();
    if (store) store.setItem("upcomingHours", String(state.upcomingHours));
  }, [state]);

  const onChange = useCallback((patch: Partial<BoardState>) => {
    setState((current) => ({ ...current, ...patch }));
  }, []);

  // optimistic renders a queued write at once and holds it as a pending
  // overlay until a read confirms it.
  const optimistic = useCallback((viewKind: CardKind, card: CardModel, fields: Record<string, unknown>) => {
    const key = cardKey(viewKind, card);
    pending.current.set(key, { fields, until: Date.now() + 30000 });
    setCards((current) =>
      current
        ? current.map((candidate) =>
            cardKey(viewKind, candidate) === key ? { ...candidate, ...fields } : candidate,
          )
        : current,
    );
  }, []);

  const ask = useCallback(<T,>(next: Dialog): Promise<T> => {
    return new Promise<T>((resolve) => {
      dialogResolve.current = resolve as (value: unknown) => void;
      setDialog(next);
    });
  }, []);

  const resolveDialog = useCallback((value: unknown) => {
    const resolve = dialogResolve.current;
    dialogResolve.current = null;
    setDialog(null);
    resolve?.(value);
  }, []);

  const markStarting = useCallback((id: string, active: boolean) => {
    setStarting((current) => {
      const next = new Set(current);
      if (active) next.add(id);
      else next.delete(id);
      return next;
    });
  }, []);

  const vaultOf = (card: CardModel) => (card as { vault: string }).vault;

  const runCard = useCallback(
    async (viewKind: CardKind, card: CardModel) => {
      const id = (card as { id: string }).id;
      const vault = vaultOf(card);
      if (id.startsWith("-")) {
        addToast("Invalid id", true);
        return;
      }
      const route = routeFor(viewKind);
      const item = card as { claude_session_id?: string; title?: string };

      // Resume short-circuit: an item that already carries a session opens the
      // hand-off modal directly.
      if (item.claude_session_id) {
        try {
          if (viewKind === "task") {
            const data = await api.resumeCommand(id, vault);
            setSession(data);
            return;
          }
          const all = await api.fetchVaults();
          const config = all.find((candidate) => candidate.name === vault);
          if (!config) throw new Error("Vault not found");
          setSession({
            session_id: item.claude_session_id,
            command: `${config.claude_script} --resume ${item.claude_session_id}`,
            working_dir: config.vault_path,
            task_title: item.title ?? "",
          });
        } catch (err) {
          addToast((err as Error).message, true);
        }
        return;
      }

      if (viewKind === "task") setLoading(true);
      markStarting(id, true);
      try {
        const data = await api.runSession(route, id, vault);
        markStarting(id, false);
        setLoading(false);
        optimistic(viewKind, card, {
          claude_session_id: data.session_id,
          claude_session_started: null,
        });
        setSession(data);
      } catch (err) {
        markStarting(id, false);
        setLoading(false);
        if (err instanceof api.ApiError && err.status === 409) {
          addToast("Launch ended by take-over — resume from the session modal");
          optimistic(viewKind, card, { claude_session_started: null });
          void loadRef.current();
          return;
        }
        addToast((err as Error).message, true);
        void loadRef.current();
      }
    },
    [addToast, markStarting, optimistic],
  );

  const takeOver = useCallback(
    async (viewKind: CardKind, card: CardModel) => {
      const id = (card as { id: string }).id;
      if (id.startsWith("-")) {
        addToast("Invalid id", true);
        return;
      }
      const confirmed = await ask<boolean>({ type: "takeover" });
      if (!confirmed) return;
      try {
        const data = await api.takeOverSession(routeFor(viewKind), id, vaultOf(card));
        setSession(data);
        addToast(
          data.terminated === false
            ? "No running process found — resume with the command below"
            : "Running process terminated — resume with the command below",
        );
        optimistic(viewKind, card, { claude_session_started: null });
        void loadRef.current();
      } catch (err) {
        addToast((err as Error).message, true);
        optimistic(viewKind, card, { claude_session_started: null });
        void loadRef.current();
      }
    },
    [addToast, ask, optimistic],
  );

  const jump = useCallback(
    async (viewKind: CardKind, card: CardModel) => {
      const id = (card as { id: string }).id;
      if (id.startsWith("-")) {
        addToast("Invalid id", true);
        return;
      }
      try {
        await api.jumpToPane(routeFor(viewKind), id, vaultOf(card));
      } catch (err) {
        addToast((err as Error).message, true);
      }
    },
    [addToast],
  );

  const toggleFlag = useCallback(
    async (viewKind: CardKind, card: CardModel) => {
      const current = Boolean((card as { flag?: boolean }).flag);
      try {
        const result = await api.updateFlag(routeFor(viewKind), (card as { id: string }).id, !current, vaultOf(card));
        optimistic(viewKind, card, { flag: result.flag });
      } catch (err) {
        addToast((err as Error).message, true);
      }
    },
    [addToast, optimistic],
  );

  const assign = useCallback(
    async (viewKind: CardKind, card: CardModel) => {
      if (viewKind === "topic") return;
      try {
        const result = await api.assignToMe(routeFor(viewKind) as "tasks" | "goals", (card as { id: string }).id, vaultOf(card));
        optimistic(viewKind, card, { assignee: result.assignee });
      } catch (err) {
        addToast((err as Error).message, true);
      }
    },
    [addToast, optimistic],
  );

  const filterByAssignee = useCallback(
    (assignee: string) => {
      setState((current) => {
        const selected = new Set(current.assignees);
        if (selected.has(assignee)) selected.delete(assignee);
        else selected.add(assignee);
        return { ...current, assignees: Array.from(selected) };
      });
    },
    [],
  );

  const openTopic = useCallback(
    async (viewKind: CardKind, card: CardModel) => {
      if (viewKind !== "topic") return;
      const topicCard = card as TopicResponse;
      setTopic({ topic: topicCard, detail: null, error: null });
      try {
        const detail = await api.fetchTopicDetail(topicCard.id, topicCard.vault);
        setTopic({ topic: topicCard, detail, error: null });
      } catch (err) {
        setTopic({ topic: topicCard, detail: null, error: (err as Error).message });
      }
    },
    [],
  );

  const navigateToBlocker = useCallback(
    (viewKind: CardKind, name: string) => {
      document.querySelectorAll(".blocked-target").forEach((el) => el.classList.remove("blocked-target"));
      const attribute = viewKind === "goal" ? "goalId" : viewKind === "topic" ? "topicId" : "taskId";
      let found = false;
      document.querySelectorAll(".task-card").forEach((el) => {
        const dataset = (el as HTMLElement).dataset as Record<string, string | undefined>;
        if (!found && dataset[attribute] === name) {
          el.classList.add("blocked-target");
          el.scrollIntoView({ block: "center" });
          found = true;
        }
      });
      if (!found) addToast(`Blocker not on the board: ${name}`, true);
    },
    [addToast],
  );

  const patchStatus = useCallback(
    async (
      viewKind: "task" | "goal",
      card: CardModel,
      status: string,
      message: string,
      closeOut?: ReasonResult,
    ) => {
      try {
        const result = await api.updateStatus(
          routeFor(viewKind) as "tasks" | "goals",
          (card as { id: string }).id,
          status,
          vaultOf(card),
          closeOut ? { reason: closeOut.reason, gate_successor: closeOut.gate_successor } : undefined,
        );
        addToast(message);
        optimistic(viewKind, card, { status: result.new_status });
      } catch (err) {
        addToast((err as Error).message, true);
      }
    },
    [addToast, optimistic],
  );

  const runTaskCommand = useCallback(
    async (card: CardModel, action: "complete_task" | "defer_task") => {
      setLoading(true);
      try {
        const data = await api.executeCommand(
          "tasks",
          (card as { id: string }).id,
          action === "complete_task" ? "complete-task" : "defer-task",
          vaultOf(card),
        );
        setLoading(false);
        if (!data.session_id) {
          if (!data.success || data.error) addToast(data.error || "Command failed", true);
          else {
            addToast(action === "defer_task" ? "Task deferred" : "Task completed");
            void loadRef.current();
          }
          return;
        }
        setSession(data);
      } catch (err) {
        setLoading(false);
        addToast((err as Error).message, true);
      }
    },
    [addToast],
  );

  const dispatchMenu = useCallback(
    async (viewKind: CardKind, card: CardModel, action: MenuAction) => {
      setMenu(null);
      switch (action) {
        case "clear_session": {
          if (viewKind === "topic") return;
          try {
            await api.clearSession(routeFor(viewKind) as "tasks" | "goals", (card as { id: string }).id, vaultOf(card));
            optimistic(viewKind, card, { claude_session_id: null, claude_session_started: null });
          } catch (err) {
            addToast((err as Error).message, true);
          }
          return;
        }
        case "complete_goal":
        case "defer_goal": {
          try {
            await api.executeCommand(
              "goals",
              (card as { id: string }).id,
              action === "complete_goal" ? "complete-goal" : "defer-goal",
              vaultOf(card),
            );
            addToast(action === "complete_goal" ? "Goal completed" : "Goal deferred to tomorrow");
            void loadRef.current();
          } catch (err) {
            addToast((err as Error).message, true);
          }
          return;
        }
        case "abort_goal":
        case "abort_task": {
          const verbKind = action === "abort_goal" ? "goal" : "task";
          const closeOut = await ask<ReasonResult | null>({ type: "reason", kind: verbKind, verb: "abort" });
          if (closeOut === null) return;
          await patchStatus(
            verbKind,
            card,
            "aborted",
            verbKind === "goal" ? "Goal aborted" : "Task aborted",
            closeOut,
          );
          return;
        }
        case "hold_goal":
          await patchStatus("goal", card, "hold", "Goal on hold");
          return;
        case "resume_goal":
          await patchStatus("goal", card, "in_progress", "Goal resumed");
          return;
        case "hold_task":
          await patchStatus("task", card, "hold", "Task on hold");
          return;
        case "resume_task":
          await patchStatus("task", card, "in_progress", "Task resumed");
          return;
        case "complete_task":
        case "defer_task":
          await runTaskCommand(card, action);
          return;
      }
    },
    [addToast, ask, optimistic, patchStatus, runTaskCommand],
  );

  // A drop writes a task's phase. Dropping a todo task into Planning is the
  // operator's approval, so it asks first and may carry answers.
  const dropPhase = useCallback(
    async (taskID: string, phase: string) => {
      const card = cards?.find((candidate) => cardKey("task", candidate) === taskID);
      if (!card) {
        addToast("Item not found", true);
        return;
      }
      let answers: ApproveResult["answers"] = [];
      if (phase === "planning" && (card as { phase?: string }).phase === "todo") {
        const questions = ((card as { open_questions?: { index: number; text: string }[] }).open_questions ?? []);
        const approval = await ask<ApproveResult | null>({ type: "approve", questions });
        if (approval === null) return;
        answers = approval.answers;
      }
      try {
        const result = await api.updatePhase(taskID, phase, vaultOf(card), answers);
        optimistic("task", card, { phase: result.phase });
      } catch (err) {
        addToast((err as Error).message, true);
      }
    },
    [addToast, ask, cards, optimistic],
  );

  const refresh = useCallback(async () => {
    try {
      const result = (await api.reloadConfig()) as { vaults?: unknown[] };
      await refreshVaults();
      await loadRef.current();
      const options = await api.fetchAssignees(state.vaults);
      setAssigneeOptions({ named: options.named ?? [], hasUnassigned: Boolean(options.has_unassigned) });
      addToast(`Config reloaded — ${result.vaults?.length ?? 0} vaults`);
    } catch (err) {
      addToast((err as Error).message, true);
      await loadRef.current();
    }
  }, [addToast, refreshVaults, state.vaults]);

  const actions: CardActions = useMemo(
    () => ({
      onRun: (viewKind, card) => void runCard(viewKind, card),
      onJump: (viewKind, card) => void jump(viewKind, card),
      onFlag: (viewKind, card) => void toggleFlag(viewKind, card),
      onMenu: (viewKind, card, anchor) => setMenu({ kind: viewKind, card, anchor }),
      onAssign: (viewKind, card) => void assign(viewKind, card),
      onAssigneeFilter: filterByAssignee,
      onDetail: (viewKind, card) => void openTopic(viewKind, card),
      onBlocker: navigateToBlocker,
      onTakeOver: (viewKind, card) => void takeOver(viewKind, card),
    }),
    [assign, filterByAssignee, jump, navigateToBlocker, openTopic, runCard, takeOver, toggleFlag],
  );

  const noSource =
    state.vaults.length > 0 &&
    vaults.some((vault) => state.vaults.includes(vault.name) && vault.source === false);
  const unserved = vaults
    .filter((vault) => state.vaults.includes(vault.name) && vault.source === false)
    .map((vault) => vault.name);

  const closeTopic = useCallback(() => setTopic(null), []);

  return (
    <>
      <div
        id="vault-api-unavailable"
        className={`banner${sourceUnavailable ? "" : " hidden"}`}
        data-testid="vault-api-unavailable"
      >
        vault-api unavailable — showing last-known data
      </div>
      <div id="vault-no-source" className={`banner${noSource ? "" : " hidden"}`} data-testid="vault-no-source">
        {unserved.length === 1
          ? `No source for vault "${unserved[0]}" — the board cannot serve it`
          : `No source for vaults ${unserved.map((name) => `"${name}"`).join(", ")} — the board cannot serve them`}
      </div>

      <div className="app">
        <Toolbar
          state={state}
          vaults={vaults}
          assigneeOptions={assigneeOptions}
          connected={connected}
          onChange={onChange}
          onRefresh={() => void refresh()}
        />

        {error !== null && cards === null && <p className="err">Could not read the board: {error}</p>}
        {cards === null && error === null && <p className="subtitle">Loading…</p>}
        {cards !== null && (
          <Board
            kind={kind}
            cards={cards}
            sort={state.sort}
            actions={actions}
            now={now}
            starting={starting}
            activeAssignees={state.assignees}
            onDropPhase={(taskID, phase) => void dropPhase(taskID, phase)}
          />
        )}
      </div>

      {menu && (
        <Menu
          state={menu}
          onClose={() => setMenu(null)}
          onSelect={(action) => void dispatchMenu(menu.kind, menu.card, action)}
        />
      )}

      {dialog?.type === "takeover" && <TakeOverModal onResolve={(confirmed) => resolveDialog(confirmed)} />}
      {dialog?.type === "reason" && <ReasonModal dialog={dialog} onResolve={(result) => resolveDialog(result)} />}
      {dialog?.type === "approve" && <ApproveModal dialog={dialog} onResolve={(result) => resolveDialog(result)} />}
      {session && <SessionModal session={session} onClose={() => setSession(null)} />}
      {loading && <LoadingModal onClose={() => setLoading(false)} />}
      {topic && (
        <TopicModal
          topic={topic.topic}
          detail={topic.detail}
          error={topic.error}
          onClose={closeTopic}
          onNavigate={(view) => {
            closeTopic();
            onChange({ view });
          }}
        />
      )}

      <Toasts toasts={toasts} />
    </>
  );
}

// The generated types are referenced by the fetchers' return values; keep the
// imports honest without an unused-import error.
export type BoardTypes = TaskResponse | GoalResponse | TopicResponse | CardModel | SortKey | VaultResponse;
