// URL and localStorage persistence for the board's filter state. Kept apart
// from the model: the model is pure vocabulary, this module is the one place
// that knows the address bar's spelling.
import { defaultStatuses, type SortKey, type View } from "./model";

export interface BoardState {
  vaults: string[];
  assignees: string[];
  statuses: string[];
  goals: string[];
  liveOnly: boolean;
  view: View;
  sort: SortKey;
  upcomingHours: number;
}

const VIEWS: View[] = ["tasks", "goals", "topics"];

function isView(value: string | null): value is View {
  return value !== null && (VIEWS as string[]).includes(value);
}

function isSort(value: string | null): value is SortKey {
  return value === "priority" || value === "modified" || value === "default";
}

// clampUpcoming keeps the window inside the control's 0–168 range.
export function clampUpcoming(hours: number): number {
  if (!Number.isFinite(hours)) return 8;
  if (hours < 0) return 0;
  if (hours > 168) return 168;
  return hours;
}

// readUpcoming restores the persisted window, defaulting to 8 hours.
export function readUpcoming(storage: Pick<Storage, "getItem"> | undefined): number {
  const raw = storage?.getItem("upcomingHours");
  if (raw === null || raw === undefined) return 8;
  const parsed = Number.parseInt(raw, 10);
  return Number.isNaN(parsed) ? 8 : clampUpcoming(parsed);
}

// parseSearch reads the filter state out of a query string. The status default
// is kind-aware and applies only when the URL carries no `status` at all.
export function parseSearch(search: string, upcomingHours: number): BoardState {
  const params = new URLSearchParams(search);
  const viewParam = params.get("view");
  const view: View = isView(viewParam) ? viewParam : "tasks";
  const statusParams = params.getAll("status");
  const sortParam = params.get("sort");
  return {
    vaults: params.getAll("vault"),
    assignees: params.getAll("assignee"),
    statuses: statusParams.length > 0 ? statusParams : defaultStatuses(view),
    goals: params.getAll("goal"),
    liveOnly: params.get("session_live") === "true",
    view,
    sort: isSort(sortParam) ? sortParam : "default",
    upcomingHours,
  };
}

// buildSearch rewrites the query string for the current state. `view` and
// `sort` are always emitted so a reload lands in the same view and order; the
// status list is omitted only when it is empty (the backend default applies).
export function buildSearch(state: BoardState): string {
  const params = new URLSearchParams();
  for (const vault of state.vaults) params.append("vault", vault);
  for (const assignee of state.assignees) params.append("assignee", assignee);
  for (const status of state.statuses) params.append("status", status);
  for (const goal of state.goals) params.append("goal", goal);
  if (state.liveOnly) params.set("session_live", "true");
  params.set("view", state.view);
  params.set("sort", state.sort);
  const encoded = params.toString();
  return encoded === "" ? "" : `?${encoded}`;
}

// persistVaults mirrors the selection into localStorage. The all-vaults view
// removes the key rather than storing an empty list.
export function persistVaults(
  storage: Pick<Storage, "setItem" | "removeItem"> | undefined,
  vaults: string[],
): void {
  if (!storage) return;
  if (vaults.length === 0) {
    storage.removeItem("selectedVaults");
    return;
  }
  storage.setItem("selectedVaults", JSON.stringify(vaults));
}

// restoreVaults reads the persisted selection, migrating the legacy
// single-vault key. Unknown vault names are dropped against the live list.
export function restoreVaults(
  storage: Pick<Storage, "getItem" | "setItem" | "removeItem"> | undefined,
  known: string[],
): string[] {
  if (!storage) return [];
  const saved = storage.getItem("selectedVaults");
  if (saved !== null) {
    try {
      const parsed: unknown = JSON.parse(saved);
      if (Array.isArray(parsed)) {
        return parsed.filter((name): name is string => typeof name === "string" && known.includes(name));
      }
    } catch {
      // Invalid JSON — fall through to the legacy key.
    }
  }
  const legacy = storage.getItem("selectedVault");
  if (legacy && known.includes(legacy)) {
    storage.setItem("selectedVaults", JSON.stringify([legacy]));
    storage.removeItem("selectedVault");
    return [legacy];
  }
  return [];
}
