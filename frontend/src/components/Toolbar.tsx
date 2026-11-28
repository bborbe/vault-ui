import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import type { VaultResponse } from "../types";
import { ALL_STATUSES, type SortKey, type View } from "../model";
import type { BoardState } from "../state";
import { clampUpcoming } from "../state";

const VIEWS: { view: View; label: string }[] = [
  { view: "tasks", label: "Tasks" },
  { view: "goals", label: "Goals" },
  { view: "topics", label: "Topics" },
];

const UPCOMING_OPTIONS = [
  { value: 0, label: "Upcoming: Off" },
  { value: 2, label: "Upcoming: 2h" },
  { value: 4, label: "Upcoming: 4h" },
  { value: 8, label: "Upcoming: 8h" },
  { value: 12, label: "Upcoming: 12h" },
  { value: 24, label: "Upcoming: 24h" },
];

const SORT_OPTIONS: { value: SortKey; label: string }[] = [
  { value: "default", label: "Sort: Default" },
  { value: "priority", label: "Sort: Priority" },
  { value: "modified", label: "Sort: Last modified" },
];

function truncate(text: string, max: number): string {
  return text.length > max ? `${text.slice(0, max)}...` : text;
}

// Dropdown is the one toggle/panel shell the three header selectors share:
// click-outside and Escape both close it. The panel stays mounted with the
// `hidden` class, because the e2e suite counts its checkboxes without opening
// it.
function Dropdown({
  containerID,
  toggleID,
  labelID,
  dropdownID,
  containerClass,
  toggleClass,
  arrowClass,
  label,
  children,
}: {
  containerID: string;
  toggleID: string;
  labelID: string;
  dropdownID: string;
  containerClass: string;
  toggleClass: string;
  arrowClass: string;
  label: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDocumentClick = (event: MouseEvent) => {
      if (ref.current && !ref.current.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("click", onDocumentClick);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("click", onDocumentClick);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  return (
    <div className={containerClass} id={containerID} ref={ref}>
      <button type="button" className={toggleClass} id={toggleID} onClick={() => setOpen((value) => !value)}>
        <span id={labelID}>{label}</span>
        <span className={arrowClass}>▾</span>
      </button>
      <div className={`${containerClass}-dropdown${open ? "" : " hidden"}`} id={dropdownID}>
        {children}
      </div>
    </div>
  );
}

function CheckRow({
  className,
  id,
  value,
  checked,
  label,
  onChange,
  extra,
}: {
  className: string;
  id: string;
  value: string;
  checked: boolean;
  label: string;
  onChange: (checked: boolean) => void;
  extra?: ReactNode;
}) {
  return (
    <div className={`${className}-item${checked ? " checked" : ""}`}>
      <input
        type="checkbox"
        id={id}
        value={value}
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
      />
      <label htmlFor={id}>{label}</label>
      {extra}
    </div>
  );
}

export default function Toolbar({
  state,
  vaults,
  assigneeOptions,
  connected,
  onChange,
  onRefresh,
}: {
  state: BoardState;
  vaults: VaultResponse[];
  assigneeOptions: { named: string[]; hasUnassigned: boolean };
  connected: boolean;
  onChange: (patch: Partial<BoardState>) => void;
  onRefresh: () => void;
}) {
  const selectedVaults = new Set(state.vaults.length === 0 ? vaults.map((v) => v.name) : state.vaults);
  const allVaultsSelected = state.vaults.length === 0;

  const vaultLabel =
    state.vaults.length === 0 ? "All" : truncate(state.vaults.join(", "), 20);

  const allStatusesSelected = state.statuses.length === ALL_STATUSES.length;
  const statusLabel =
    state.statuses.length === 0
      ? "None"
      : allStatusesSelected
        ? "All"
        : truncate(state.statuses.join(", "), 30);

  // Assignee options include any selected value absent from the live set, so a
  // bookmarked filter never loses its own row.
  const named = new Set(assigneeOptions.named);
  let hasUnassigned = assigneeOptions.hasUnassigned;
  for (const value of state.assignees) {
    if (value === "") hasUnassigned = true;
    else named.add(value);
  }
  const namedAssignees = Array.from(named).sort((a, b) => a.localeCompare(b));
  const assigneeLabel =
    state.assignees.length === 0
      ? "All"
      : truncate(
          state.assignees.map((value) => (value === "" ? "Unassigned" : value)).join(", "),
          30,
        );

  const toggleVault = (name: string) => {
    const next = new Set(state.vaults.length === 0 ? vaults.map((v) => v.name) : state.vaults);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    // None checked, or every vault checked, both mean the all-vaults view.
    const list = Array.from(next);
    onChange({ vaults: list.length === 0 || list.length === vaults.length ? [] : list });
  };

  const toggleStatus = (status: string) => {
    const selected = new Set(state.statuses);
    if (selected.has(status)) selected.delete(status);
    else selected.add(status);
    onChange({ statuses: ALL_STATUSES.filter((value) => selected.has(value)) });
  };

  const toggleAssignee = (value: string) => {
    const selected = new Set(state.assignees);
    if (selected.has(value)) selected.delete(value);
    else selected.add(value);
    onChange({ assignees: Array.from(selected) });
  };

  return (
    <header>
      <h1>Vault UI</h1>

      <div className="view-toggle" data-testid="view-toggle" role="tablist" aria-label="View">
        {VIEWS.map(({ view, label }) => (
          <button
            key={view}
            type="button"
            className={`view-toggle-btn${state.view === view ? " active" : ""}`}
            data-view={view}
            aria-selected={state.view === view}
            onClick={() => onChange({ view })}
          >
            {label}
          </button>
        ))}
      </div>

      <div className="header-controls">
        <Dropdown
          containerID="vault-selector"
          toggleID="vault-selector-toggle"
          labelID="vault-selector-label"
          dropdownID="vault-selector-dropdown"
          containerClass="vault-selector"
          toggleClass="vault-selector-toggle"
          arrowClass="vault-selector-arrow"
          label={vaultLabel}
        >
          <CheckRow
            className="vault-selector"
            id="vault-cb-all"
            value="__all__"
            checked={allVaultsSelected}
            label="All"
            onChange={() => onChange({ vaults: [] })}
          />
          <hr className="vault-selector-separator" />
          {vaults.map((vault) => (
            <CheckRow
              key={vault.name}
              className="vault-selector"
              id={`vault-cb-${vault.name}`}
              value={vault.name}
              checked={selectedVaults.has(vault.name)}
              label={vault.name}
              onChange={() => toggleVault(vault.name)}
              extra={
                <button
                  type="button"
                  className="vault-only-btn"
                  data-vault={vault.name}
                  onClick={(event) => {
                    event.stopPropagation();
                    onChange({ vaults: [vault.name] });
                  }}
                >
                  Only
                </button>
              }
            />
          ))}
        </Dropdown>

        <Dropdown
          containerID="status-selector"
          toggleID="status-selector-toggle"
          labelID="status-selector-label"
          dropdownID="status-selector-dropdown"
          containerClass="status-selector"
          toggleClass="status-selector-toggle"
          arrowClass="status-selector-arrow"
          label={statusLabel}
        >
          <CheckRow
            className="status-selector"
            id="status-cb-all"
            value="__all__"
            checked={allStatusesSelected}
            label="All"
            onChange={() =>
              onChange({ statuses: allStatusesSelected ? [] : [...ALL_STATUSES] })
            }
          />
          <hr className="status-selector-separator" />
          {ALL_STATUSES.map((status) => (
            <CheckRow
              key={status}
              className="status-selector"
              id={`status-cb-${status}`}
              value={status}
              checked={state.statuses.includes(status)}
              label={status}
              onChange={() => toggleStatus(status)}
            />
          ))}
        </Dropdown>

        <Dropdown
          containerID="assignee-selector"
          toggleID="assignee-selector-toggle"
          labelID="assignee-selector-label"
          dropdownID="assignee-selector-dropdown"
          containerClass="assignee-selector"
          toggleClass="assignee-selector-toggle"
          arrowClass="assignee-selector-arrow"
          label={assigneeLabel}
        >
          <CheckRow
            className="assignee-selector"
            id="assignee-cb-all"
            value="__all__"
            checked={state.assignees.length === 0}
            label="All"
            // "All" clears the filter; unchecking it directly is a no-op.
            onChange={(checked) => {
              if (checked) onChange({ assignees: [] });
            }}
          />
          <hr className="assignee-selector-separator" />
          {namedAssignees.map((name, index) => (
            <CheckRow
              key={name}
              className="assignee-selector"
              id={`assignee-cb-${index}`}
              value={name}
              checked={state.assignees.includes(name)}
              label={name}
              onChange={() => toggleAssignee(name)}
            />
          ))}
          {hasUnassigned && (
            <CheckRow
              className="assignee-selector"
              id={`assignee-cb-${namedAssignees.length}`}
              value=""
              checked={state.assignees.includes("")}
              label="Unassigned"
              onChange={() => toggleAssignee("")}
            />
          )}
        </Dropdown>

        <select
          id="upcoming-window"
          title="How far ahead to show upcoming (greyed-out) deferred tasks"
          value={state.upcomingHours}
          onChange={(event) => onChange({ upcomingHours: clampUpcoming(Number(event.target.value)) })}
        >
          {UPCOMING_OPTIONS.map(({ value, label }) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>

        <select
          id="sort-select"
          title="Order cards within each column"
          value={state.sort}
          onChange={(event) => onChange({ sort: event.target.value as SortKey })}
        >
          {SORT_OPTIONS.map(({ value, label }) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>

        <label id="live-session-label" title="Show only items whose Claude session is running right now">
          <input
            type="checkbox"
            id="live-session-filter"
            checked={state.liveOnly}
            onChange={(event) => onChange({ liveOnly: event.target.checked })}
          />{" "}
          Live session
        </label>

        <span
          id="ws-status"
          className={`ws-status${connected ? "" : " disconnected"}`}
          title={connected ? "Live updates connected" : "Live updates disconnected"}
        >
          ●
        </span>

        <button type="button" id="refresh-btn" onClick={onRefresh}>
          ↻ Refresh
        </button>
      </div>
    </header>
  );
}
