// The single transport seam. Every fetch lives here; a component that calls
// fetch directly is the seam leaking, and the next transport change has to hunt
// it down. Components read the model this module returns.
import type {
  AssigneesResponse,
  AssignResponse,
  GoalResponse,
  PhaseUpdateResponse,
  SessionResponse,
  StatusUpdateResponse,
  TaskResponse,
  TopicDetailResponse,
  TopicResponse,
  VaultResponse,
} from "./types";

export const TASKS_URL = "/api/tasks";
export const GOALS_URL = "/api/goals";
export const TOPICS_URL = "/api/topics";
export const VAULTS_URL = "/api/vaults";
export const ASSIGNEES_URL = "/api/assignees";
export const SOURCE_STATUS_URL = "/api/source-status";
export const CONFIG_RELOAD_URL = "/api/config/reload";
export const EVENTS_URL = "/api/events";

// BoardQuery is the filter set every list endpoint accepts. Empty fields are
// omitted from the query string rather than sent blank.
export interface BoardQuery {
  vault?: string[];
  status?: string[];
  phase?: string[];
  assignee?: string[];
  goal?: string[];
  upcomingHours?: number;
  sessionLive?: boolean;
}

function queryString(query: BoardQuery): string {
  const params = new URLSearchParams();
  for (const vault of query.vault ?? []) params.append("vault", vault);
  for (const status of query.status ?? []) params.append("status", status);
  for (const phase of query.phase ?? []) params.append("phase", phase);
  for (const assignee of query.assignee ?? []) params.append("assignee", assignee);
  for (const goal of query.goal ?? []) params.append("goal", goal);
  if (query.upcomingHours !== undefined) {
    params.set("upcoming_hours", String(query.upcomingHours));
  }
  if (query.sessionLive) params.set("session_live", "true");
  const encoded = params.toString();
  return encoded === "" ? "" : `?${encoded}`;
}

// ApiError carries the HTTP status so a caller can tell a 409 take-over apart
// from a real failure.
export class ApiError extends Error {
  status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(url, { signal });
  if (!response.ok) {
    throw new ApiError(`GET ${url} failed: ${response.status}`, response.status);
  }
  return (await response.json()) as T;
}

async function sendJSON<T>(
  method: string,
  url: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(url, {
    method,
    signal,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!response.ok) {
    throw new ApiError(`${method} ${url} failed: ${response.status}`, response.status);
  }
  // A 204 carries no body; callers that expect one use a route that returns it.
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export function fetchTasks(query: BoardQuery, signal?: AbortSignal) {
  return getJSON<TaskResponse[]>(`${TASKS_URL}${queryString(query)}`, signal);
}

export function fetchGoals(query: BoardQuery, signal?: AbortSignal) {
  return getJSON<GoalResponse[]>(`${GOALS_URL}${queryString(query)}`, signal);
}

export function fetchTopics(query: BoardQuery, signal?: AbortSignal) {
  return getJSON<TopicResponse[]>(`${TOPICS_URL}${queryString(query)}`, signal);
}

export function fetchTopicDetail(id: string, vault: string, signal?: AbortSignal) {
  return getJSON<TopicDetailResponse>(
    `${TOPICS_URL}/${encodeURIComponent(id)}?vault=${encodeURIComponent(vault)}`,
    signal,
  );
}

export function fetchVaults(signal?: AbortSignal) {
  return getJSON<VaultResponse[]>(VAULTS_URL, signal);
}

export function fetchAssignees(vaults: string[], signal?: AbortSignal) {
  const params = new URLSearchParams();
  for (const vault of vaults) params.append("vault", vault);
  const encoded = params.toString();
  return getJSON<AssigneesResponse>(
    `${ASSIGNEES_URL}${encoded === "" ? "" : `?${encoded}`}`,
    signal,
  );
}

// SourceStatus is the vault-api availability verdict the outage banner reads.
export interface SourceStatus {
  unavailable: boolean;
}

export function fetchSourceStatus(signal?: AbortSignal) {
  return getJSON<SourceStatus>(SOURCE_STATUS_URL, signal);
}

export function reloadConfig(signal?: AbortSignal) {
  return sendJSON<unknown>("POST", CONFIG_RELOAD_URL, undefined, signal);
}

// sessionURL builds the per-kind route the board's mutations share.
function sessionURL(kind: "tasks" | "goals" | "topics", id: string, action: string, vault: string) {
  return `/api/${kind}/${encodeURIComponent(id)}/${action}?vault=${encodeURIComponent(vault)}`;
}

export function assignToMe(kind: "tasks" | "goals", id: string, vault: string) {
  return sendJSON<AssignResponse>("PATCH", sessionURL(kind, id, "assign-to-me", vault));
}

export function updatePhase(
  taskID: string,
  phase: string,
  vault: string,
  answers?: { index: number; answer: string }[],
) {
  return sendJSON<PhaseUpdateResponse>(
    "PATCH",
    sessionURL("tasks", taskID, "phase", vault),
    answers && answers.length > 0 ? { phase, answers } : { phase },
  );
}

export function updateFlag(
  kind: "tasks" | "goals" | "topics",
  id: string,
  flag: boolean,
  vault: string,
) {
  return sendJSON<{ flag: boolean }>("PATCH", sessionURL(kind, id, "flag", vault), {
    kind: kind.slice(0, -1),
    flag,
  });
}

export function updateStatus(
  kind: "tasks" | "goals",
  id: string,
  status: string,
  vault: string,
  extra?: { reason?: string; gate_successor?: string },
) {
  return sendJSON<StatusUpdateResponse>("PATCH", sessionURL(kind, id, "status", vault), {
    status,
    ...extra,
  });
}

export function runSession(kind: "tasks" | "goals" | "topics", id: string, vault: string) {
  return sendJSON<SessionResponse>("POST", sessionURL(kind, id, "run", vault));
}

export function takeOverSession(kind: "tasks" | "goals" | "topics", id: string, vault: string) {
  return sendJSON<SessionResponse>("POST", sessionURL(kind, id, "take-over", vault));
}

export function jumpToPane(kind: "tasks" | "goals" | "topics", id: string, vault: string) {
  return sendJSON<void>("POST", sessionURL(kind, id, "jump", vault));
}

export function clearSession(kind: "tasks" | "goals", id: string, vault: string) {
  return sendJSON<unknown>("DELETE", sessionURL(kind, id, "session", vault));
}

export function resumeCommand(taskID: string, vault: string, signal?: AbortSignal) {
  return getJSON<SessionResponse>(sessionURL("tasks", taskID, "resume-command", vault), signal);
}

export function executeCommand(
  kind: "tasks" | "goals",
  id: string,
  command: string,
  vault: string,
) {
  return sendJSON<SessionResponse>(
    "POST",
    sessionURL(kind, id, "execute-command", vault),
    { command },
  );
}

// subscribeBoard listens for board changes. Server-Sent Events, not a duplex
// socket: the page never streams data back, and the browser reconnects on its
// own. Every event is a wake-up
// ("read the board again") rather than a data frame, so one code path builds
// the model from the JSON endpoints. jsdom (Vitest) has no EventSource, so the
// guard is load-bearing rather than defensive noise.
export function subscribeBoard(
  onChange: () => void,
  onStatus?: (connected: boolean) => void,
): () => void {
  if (typeof EventSource === "undefined") {
    return () => undefined;
  }
  const source = new EventSource(EVENTS_URL);
  let hasOpened = false;
  source.addEventListener("board", onChange);
  source.addEventListener("open", () => {
    onStatus?.(true);
    // Events emitted while the stream was down are never replayed, so a
    // reconnect re-reads the board. The first connect is skipped: the initial
    // load has just fetched the same data.
    if (hasOpened) onChange();
    hasOpened = true;
  });
  source.addEventListener("error", () => onStatus?.(false));
  return () => source.close();
}
