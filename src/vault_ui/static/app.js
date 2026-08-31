// vault-ui Kanban Board

let currentVault = null; // null = "All", or vault name
let currentAssignees = [];
let currentStatuses = ['in_progress', 'hold', 'completed']; // default — overridden by ?status= URL param; hold shown by default so parked/blocked work isn't forgotten
let currentGoals = []; // goal filter from URL — empty means no filter
let upcomingHours = 8; // 0 = hide all deferred tasks; persists in localStorage
// Distinct assignees across the selected vaults — sourced from /api/assignees,
// refreshed on startup and on every vault-selector change. Read by computeAssigneeOptions.
let availableAssignees = { named: [], hasUnassigned: false };
const ALL_STATUSES = ['next', 'in_progress', 'backlog', 'completed', 'hold', 'aborted']; // closed enum, fixed display order
let tasksCache = {}; // Map of task ID -> task data
let goalsCache = {}; // Map of goal ID -> goal data (mirrors tasksCache)
let currentView = 'tasks'; // 'tasks' | 'goals' — synced to ?view= URL param, default 'tasks'
let currentSort = 'default'; // 'default' | 'priority' | 'modified' — column sort key, synced to ?sort= URL param
let currentGroupBy = 'phase'; // 'phase' | 'status' — derived from currentView (tasks→phase, goals→status); not user-selectable
let ws = null; // WebSocket connection
let startingTasks = new Set(); // Track tasks currently being started
let startingGoals = new Set(); // Track goals currently being started (mirrors startingTasks)

const POLL_INTERVAL_MS = 60000; // Fallback polling every 60 seconds

async function parseErrorResponse(response) {
    // Backend returns FastAPI HTTPException → {"detail": "..."} as application/json.
    // Try JSON first; fall back to text for non-JSON responses (proxy errors, network failures).
    try {
        const body = await response.json();
        if (body && typeof body.detail === 'string') return body.detail;
        return JSON.stringify(body);
    } catch {
        try {
            const text = await response.text();
            return text || `HTTP ${response.status}`;
        } catch {
            return `HTTP ${response.status}`;
        }
    }
}

// Load tasks on page load
document.addEventListener('DOMContentLoaded', () => {
    // Rename in_progress column to the new canonical phase name.
    // HTML is not modified; the rename happens at runtime so only app.js changes.
    const execColumn = document.getElementById('cards-in_progress');
    if (execColumn) {
        execColumn.id = 'cards-execution';
        const h2 = execColumn.closest('.kanban-column').querySelector('h2');
        if (h2) h2.textContent = 'Execution';
    }
    parseURLParams();
    renderColumnHeaders();  // builds the column DOM based on currentGroupBy + currentView
    loadVaults();
    setupEventListeners();
    connectWebSocket();
    startPolling();
});

// Fallback polling in case WebSocket misses updates.
// Routes through loadCurrentView() so the periodic poll does NOT clobber
// the Goals view with task cards when the operator is on ?view=goals
// (spec 014 AC#1 — periodic poll is view-aware).
function startPolling() {
    setInterval(() => {
        console.log('Polling for updates...');
        loadCurrentView();
    }, POLL_INTERVAL_MS);
}

function parseURLParams() {
    const params = new URLSearchParams(window.location.search);

    // Parse vault parameter(s)
    const vaultParams = params.getAll('vault');
    if (vaultParams.length === 0) {
        currentVault = null; // Show all
    } else if (vaultParams.length === 1) {
        currentVault = vaultParams[0];
    } else {
        currentVault = vaultParams; // Multiple vaults
    }

    // Parse assignee parameter(s) — supports repeated form (?assignee=a&assignee=b)
    currentAssignees = params.getAll('assignee');

    // Parse goal parameter(s) — supports repeated form (?goal=A&goal=B)
    currentGoals = params.getAll('goal');

    // Parse view parameter — single string, not a list. Must precede the
    // status-default block below so the kind-aware default knows which view it's on.
    const viewParam = params.get('view');
    if (viewParam === 'goals' || viewParam === 'tasks') {
        currentView = viewParam;
    } else {
        currentView = 'tasks';
    }

    // Parse status parameter(s) — supports repeated form and comma-separated form
    // (backend handles comma-split server-side). Absent param falls back to a
    // KIND-AWARE default:
    //   - Tasks view: ['in_progress', 'completed'] (current behaviour — operator
    //     usually wants to see active + recent wins, not planning queue).
    //   - Goals view: ['backlog', 'next', 'in_progress', 'hold', 'completed'] — matches the
    //     four visible status columns on the Goals board, so each column has its
    //     items by default instead of two empty columns confusing the operator.
    const statusParams = params.getAll('status');
    if (statusParams.length > 0) {
        currentStatuses = statusParams;
    } else if (currentView === 'goals') {
        currentStatuses = ['backlog', 'next', 'in_progress', 'hold', 'completed'];
    }
    // else: keep the module-level default ['in_progress', 'hold', 'completed'] for Tasks view.

    // Parse sort parameter — single string from the allowlist; absent or
    // unknown values fall back to 'default' (each view's existing ordering).
    const sortParam = params.get('sort');
    if (sortParam === 'priority' || sortParam === 'modified') {
        currentSort = sortParam;
    } else {
        currentSort = 'default';
    }

    // Grouping is derived from view: tasks→phase, goals→status.
    // The groupBy UI selector + URL param were removed (the cross-axis combinations
    // weren't useful: tasks-by-status duplicates the status filter dropdown; goals-by-phase
    // is meaningless since goals have no phase).
    currentGroupBy = currentView === 'goals' ? 'status' : 'phase';
}

function setupEventListeners() {
    document.getElementById('vault-selector-toggle').addEventListener('click', toggleVaultDropdown);
    document.addEventListener('click', handleClickOutsideVaultDropdown);
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') closeVaultDropdown();
    });
    document.getElementById('status-selector-toggle').addEventListener('click', toggleStatusDropdown);
    document.addEventListener('click', handleClickOutsideStatusDropdown);
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') closeStatusDropdown();
    });
    document.getElementById('assignee-selector-toggle').addEventListener('click', toggleAssigneeDropdown);
    document.addEventListener('click', handleClickOutsideAssigneeDropdown);
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') closeAssigneeDropdown();
    });
    document.getElementById('refresh-btn').addEventListener('click', loadCurrentView);
    document.getElementById('copy-btn').addEventListener('click', copyCommand);
    document.getElementById('close-btn').addEventListener('click', closeModal);
    setupUpcomingWindow();
    setupSortControl();
    setupModalBackdropClose();
    setupDragAndDrop();

    // View toggle: Tasks / Goals
    const viewToggle = document.querySelector('.view-toggle');
    if (viewToggle) {
        viewToggle.addEventListener('click', (e) => {
            const btn = e.target.closest('.view-toggle-btn');
            if (!btn) return;
            const newView = btn.dataset.view;
            if (newView === currentView) return;
            setView(newView);
        });
    }
    updateViewToggle();
}

// Upcoming-window dropdown: restores the persisted value, syncs the select,
// and reloads tasks on change so the backend re-applies the new cutoff.
function setupUpcomingWindow() {
    const select = document.getElementById('upcoming-window');
    if (!select) return;
    const saved = parseInt(localStorage.getItem('upcomingHours') ?? '8', 10);
    if (Number.isFinite(saved) && saved >= 0 && saved <= 168) {
        upcomingHours = saved;
        select.value = String(saved);
    }
    select.addEventListener('change', () => {
        const next = parseInt(select.value, 10);
        if (Number.isFinite(next) && next >= 0 && next <= 168) {
            upcomingHours = next;
            localStorage.setItem('upcomingHours', String(next));
            loadCurrentView();
        }
    });
}

// Sort-select: syncs the control to the current sort key, and on change
// re-renders the active view from cache (no refetch) so cards reorder
// immediately. The URL is rewritten so reloads preserve the same order.
function setupSortControl() {
    const select = document.getElementById('sort-select');
    if (!select) return;
    select.value = currentSort;
    select.addEventListener('change', () => {
        currentSort = select.value;
        updateURL();
        if (currentView === 'goals') {
            renderGoals();
        } else {
            renderTasks();
        }
    });
}

// Click on the modal backdrop (the dimmed area around the centered card)
// closes the modal. For the loading modal we forward to its close button so
// any dynamic closeHandler attached for that session still fires.
function setupModalBackdropClose() {
    const loadingModal = document.getElementById('loading-modal');
    loadingModal.addEventListener('click', (e) => {
        if (e.target === loadingModal) {
            document.getElementById('close-loading-btn').click();
        }
    });

    const sessionModal = document.getElementById('session-modal');
    sessionModal.addEventListener('click', (e) => {
        if (e.target === sessionModal) {
            closeModal();
        }
    });
}

function toggleVaultDropdown() {
    const dropdown = document.getElementById('vault-selector-dropdown');
    dropdown.classList.toggle('hidden');
}

function closeVaultDropdown() {
    const dropdown = document.getElementById('vault-selector-dropdown');
    dropdown.classList.add('hidden');
}

function handleClickOutsideVaultDropdown(e) {
    const container = document.getElementById('vault-selector');
    if (container && !container.contains(e.target)) {
        closeVaultDropdown();
    }
}

function toggleStatusDropdown() {
    const dropdown = document.getElementById('status-selector-dropdown');
    if (dropdown.classList.contains('hidden')) {
        renderStatusDropdown();
    }
    dropdown.classList.toggle('hidden');
}

function closeStatusDropdown() {
    const dropdown = document.getElementById('status-selector-dropdown');
    if (dropdown) dropdown.classList.add('hidden');
}

function handleClickOutsideStatusDropdown(e) {
    const container = document.getElementById('status-selector');
    if (container && !container.contains(e.target)) {
        closeStatusDropdown();
    }
}

function renderStatusDropdown() {
    const dropdown = document.getElementById('status-selector-dropdown');
    if (!dropdown) return;
    dropdown.innerHTML = '';

    const selectedSet = new Set(currentStatuses);
    const allChecked = ALL_STATUSES.every(s => selectedSet.has(s));

    // "All" checkbox row
    const allItem = document.createElement('div');
    allItem.className = 'status-selector-item' + (allChecked ? ' checked' : '');
    allItem.innerHTML = `<input type="checkbox" id="status-cb-all" value="__all__" ${allChecked ? 'checked' : ''}><label for="status-cb-all">All</label>`;
    allItem.querySelector('input').addEventListener('change', handleAllStatusCheckbox);
    dropdown.appendChild(allItem);

    // Separator
    const sep = document.createElement('hr');
    sep.className = 'status-selector-separator';
    dropdown.appendChild(sep);

    // One checkbox per status, in fixed enum order
    ALL_STATUSES.forEach(status => {
        const item = document.createElement('div');
        const isChecked = selectedSet.has(status);
        item.className = 'status-selector-item' + (isChecked ? ' checked' : '');
        item.innerHTML = `<input type="checkbox" id="status-cb-${status}" value="${status}" ${isChecked ? 'checked' : ''}><label for="status-cb-${status}">${status}</label>`;
        item.querySelector('input').addEventListener('change', handleStatusCheckboxChange);
        dropdown.appendChild(item);
    });
}

function handleAllStatusCheckbox() {
    const dropdown = document.getElementById('status-selector-dropdown');
    const checkboxes = Array.from(dropdown.querySelectorAll('input[type="checkbox"]:not(#status-cb-all)'));
    const allChecked = checkboxes.every(cb => cb.checked);

    if (allChecked) {
        // Uncheck everything → empty filter (backend default applies)
        checkboxes.forEach(cb => {
            cb.checked = false;
            cb.closest('.status-selector-item').classList.remove('checked');
        });
        const allCb = document.getElementById('status-cb-all');
        allCb.checked = false;
        allCb.closest('.status-selector-item').classList.remove('checked');
        currentStatuses = [];
    } else {
        // Check everything
        checkboxes.forEach(cb => {
            cb.checked = true;
            cb.closest('.status-selector-item').classList.add('checked');
        });
        const allCb = document.getElementById('status-cb-all');
        allCb.checked = true;
        allCb.closest('.status-selector-item').classList.add('checked');
        currentStatuses = [...ALL_STATUSES];
    }

    updateStatusLabel();
    updateURL();
    renderColumnHeaders();  // hold/aborted columns appear/disappear with the filter
    loadCurrentView();
}

function handleStatusCheckboxChange(e) {
    const dropdown = document.getElementById('status-selector-dropdown');
    const checkboxes = Array.from(dropdown.querySelectorAll('input[type="checkbox"]:not(#status-cb-all)'));

    e.target.closest('.status-selector-item').classList.toggle('checked', e.target.checked);

    // Rebuild currentStatuses from checked boxes, preserving the fixed enum order from ALL_STATUSES.
    const checkedSet = new Set(checkboxes.filter(cb => cb.checked).map(cb => cb.value));
    currentStatuses = ALL_STATUSES.filter(s => checkedSet.has(s));

    // Sync the "All" checkbox visual state
    const allCb = document.getElementById('status-cb-all');
    const everythingChecked = currentStatuses.length === ALL_STATUSES.length;
    allCb.checked = everythingChecked;
    allCb.closest('.status-selector-item').classList.toggle('checked', everythingChecked);

    updateStatusLabel();
    updateURL();
    renderColumnHeaders();  // hold/aborted columns appear/disappear with the filter
    loadCurrentView();
}

function updateStatusLabel() {
    const label = document.getElementById('status-selector-label');
    if (!label) return;

    if (currentStatuses.length === 0) {
        label.textContent = 'None';
    } else if (currentStatuses.length === ALL_STATUSES.length) {
        label.textContent = 'All';
    } else {
        const text = currentStatuses.join(', ');
        label.textContent = text.length > 30 ? text.slice(0, 30) + '...' : text;
    }
}

function toggleAssigneeDropdown() {
    const dropdown = document.getElementById('assignee-selector-dropdown');
    if (dropdown.classList.contains('hidden')) {
        renderAssigneeDropdown();
    }
    dropdown.classList.toggle('hidden');
}

function closeAssigneeDropdown() {
    const dropdown = document.getElementById('assignee-selector-dropdown');
    if (dropdown) dropdown.classList.add('hidden');
}

function handleClickOutsideAssigneeDropdown(e) {
    const container = document.getElementById('assignee-selector');
    if (container && !container.contains(e.target)) {
        closeAssigneeDropdown();
    }
}

function computeAssigneeOptions() {
    const named = new Set(availableAssignees.named);
    let hasUnassigned = Boolean(availableAssignees.hasUnassigned);
    // Preserve currently-selected values that are absent from the available set.
    currentAssignees.forEach(a => {
        if (a === '') {
            hasUnassigned = true;
        } else {
            named.add(a);
        }
    });
    const sortedNamed = Array.from(named).sort((a, b) => a.localeCompare(b));
    return { namedAssignees: sortedNamed, hasUnassigned };
}

function renderAssigneeDropdown() {
    const dropdown = document.getElementById('assignee-selector-dropdown');
    if (!dropdown) return;
    dropdown.innerHTML = '';

    const { namedAssignees, hasUnassigned } = computeAssigneeOptions();
    const allChecked = currentAssignees.length === 0;

    // "All" row
    const allItem = document.createElement('div');
    allItem.className = 'assignee-selector-item' + (allChecked ? ' checked' : '');
    const allCb = document.createElement('input');
    allCb.type = 'checkbox';
    allCb.id = 'assignee-cb-all';
    allCb.value = '__all__';
    allCb.checked = allChecked;
    const allLabel = document.createElement('label');
    allLabel.htmlFor = 'assignee-cb-all';
    allLabel.textContent = 'All';
    allItem.appendChild(allCb);
    allItem.appendChild(allLabel);
    allCb.addEventListener('change', handleAllAssigneeCheckbox);
    dropdown.appendChild(allItem);

    // Separator
    const sep = document.createElement('hr');
    sep.className = 'assignee-selector-separator';
    dropdown.appendChild(sep);

    // Named assignees first, alphabetical
    namedAssignees.forEach((name, idx) => {
        dropdown.appendChild(buildAssigneeRow(name, idx, currentAssignees.includes(name)));
    });

    // Unassigned row last
    if (hasUnassigned) {
        dropdown.appendChild(buildAssigneeRow('', namedAssignees.length, currentAssignees.includes('')));
    }
}

// Build a single checkbox row. Uses textContent / value (not innerHTML) for assignee strings
// to avoid HTML injection through frontmatter values.
function buildAssigneeRow(value, index, isChecked) {
    const item = document.createElement('div');
    item.className = 'assignee-selector-item' + (isChecked ? ' checked' : '');
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.id = `assignee-cb-${index}`;
    cb.value = value;
    cb.checked = isChecked;
    cb.dataset.assignee = value;
    const label = document.createElement('label');
    label.htmlFor = cb.id;
    label.textContent = value === '' ? 'Unassigned' : value;
    item.appendChild(cb);
    item.appendChild(label);
    cb.addEventListener('change', handleAssigneeCheckboxChange);
    return item;
}

function handleAllAssigneeCheckbox(e) {
    // "All" clears the filter. Clicking it while already checked is a no-op
    // (the spec lists this as the documented behavior).
    if (!e.target.checked) {
        // User unchecked the "All" row directly — re-check it; "All" cannot be turned off this way.
        e.target.checked = true;
        e.target.closest('.assignee-selector-item').classList.add('checked');
        return;
    }
    currentAssignees = [];
    updateAssigneeLabel();
    updateURL();
    loadCurrentView();
    // loadCurrentView will re-render the dropdown; no need to do it here.
}

function handleAssigneeCheckboxChange(e) {
    const value = e.target.dataset.assignee;
    e.target.closest('.assignee-selector-item').classList.toggle('checked', e.target.checked);

    const idx = currentAssignees.indexOf(value);
    if (e.target.checked && idx === -1) {
        currentAssignees.push(value);
    } else if (!e.target.checked && idx !== -1) {
        currentAssignees.splice(idx, 1);
    }

    updateAssigneeLabel();
    updateURL();
    loadCurrentView();
}

function updateAssigneeLabel() {
    const label = document.getElementById('assignee-selector-label');
    if (!label) return;
    if (currentAssignees.length === 0) {
        label.textContent = 'All';
        return;
    }
    const text = currentAssignees.map(a => a === '' ? 'Unassigned' : a).join(', ');
    label.textContent = text.length > 30 ? text.slice(0, 30) + '...' : text;
}

async function loadVaults() {
    try {
        const response = await fetch('/api/vaults');
        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }

        const vaults = await response.json();
        const dropdown = document.getElementById('vault-selector-dropdown');
        dropdown.innerHTML = '';

        // If no URL params, try loading from localStorage
        if (currentVault === null && !window.location.search) {
            // Check new key first
            const savedVaultsJson = localStorage.getItem('selectedVaults');
            if (savedVaultsJson !== null) {
                try {
                    const savedVaults = JSON.parse(savedVaultsJson);
                    if (Array.isArray(savedVaults) && savedVaults.length > 0) {
                        // Validate all saved vaults still exist
                        const validVaults = savedVaults.filter(v => vaults.find(vault => vault.name === v));
                        if (validVaults.length > 0) {
                            currentVault = validVaults.length === 1 ? validVaults[0] : validVaults;
                        }
                    }
                } catch (_) {
                    // Invalid JSON, ignore
                }
            } else {
                // Migrate old single-select key
                const oldSavedVault = localStorage.getItem('selectedVault');
                if (oldSavedVault && vaults.find(v => v.name === oldSavedVault)) {
                    currentVault = oldSavedVault;
                    localStorage.setItem('selectedVaults', JSON.stringify([oldSavedVault]));
                    localStorage.removeItem('selectedVault');
                }
            }
        }

        // Determine which vaults are selected
        const selectedSet = new Set();
        if (currentVault === null) {
            vaults.forEach(v => selectedSet.add(v.name));
        } else if (Array.isArray(currentVault)) {
            currentVault.forEach(v => selectedSet.add(v));
        } else {
            selectedSet.add(currentVault);
        }

        // Build "All" checkbox item
        const allItem = document.createElement('div');
        allItem.className = 'vault-selector-item' + (selectedSet.size === vaults.length ? ' checked' : '');
        const allChecked = currentVault === null;
        allItem.innerHTML = `<input type="checkbox" id="vault-cb-all" value="__all__" ${allChecked ? 'checked' : ''}><label for="vault-cb-all">All</label>`;
        allItem.querySelector('input').addEventListener('change', handleAllVaultCheckbox);
        dropdown.appendChild(allItem);

        // Separator
        const sep = document.createElement('hr');
        sep.className = 'vault-selector-separator';
        dropdown.appendChild(sep);

        // Individual vault checkboxes
        vaults.forEach(vault => {
            const item = document.createElement('div');
            const isChecked = selectedSet.has(vault.name);
            item.className = 'vault-selector-item' + (isChecked ? ' checked' : '');
            item.innerHTML = `<input type="checkbox" id="vault-cb-${vault.name}" value="${vault.name}" ${isChecked ? 'checked' : ''}><label for="vault-cb-${vault.name}">${vault.name}</label><button class="vault-only-btn" data-vault="${vault.name}">Only</button>`;
            item.querySelector('input').addEventListener('change', handleVaultCheckboxChange);
            item.querySelector('.vault-only-btn').addEventListener('click', (e) => {
                e.stopPropagation();
                const vaultName = e.target.dataset.vault;
                const dropdown = document.getElementById('vault-selector-dropdown');
                const checkboxes = Array.from(dropdown.querySelectorAll('input[type="checkbox"]:not(#vault-cb-all)'));
                checkboxes.forEach(cb => {
                    cb.checked = cb.value === vaultName;
                    cb.closest('.vault-selector-item').classList.toggle('checked', cb.value === vaultName);
                });
                const allCb = document.getElementById('vault-cb-all');
                allCb.checked = false;
                allCb.closest('.vault-selector-item').classList.remove('checked');
                currentVault = vaultName;
                saveVaultSelection();
                updateVaultLabel();
                updateURL();
                loadAssignees();
                loadCurrentView();
            });
            dropdown.appendChild(item);
        });

        updateVaultLabel();
        updateStatusLabel();
        updateAssigneeLabel();

        // Load assignee options before tasks so the dropdown renders against the full set on first paint.
        await loadAssignees();

        // Load the active view (single fetch; no flicker)
        await loadCurrentView();
    } catch (error) {
        console.error('Failed to load vaults:', error);
        showToast(error.message, true);
    }
}

async function loadAssignees() {
    try {
        const params = new URLSearchParams();
        if (currentVault === null) {
            // No vault param = all vaults; matches loadTasks behavior.
        } else if (Array.isArray(currentVault)) {
            currentVault.forEach(v => params.append('vault', v));
        } else {
            params.set('vault', currentVault);
        }
        const url = params.toString() ? `/api/assignees?${params.toString()}` : '/api/assignees';
        const response = await fetch(url);
        if (!response.ok) {
            console.warn(`Failed to load assignees: HTTP ${response.status}`);
            return;
        }
        const data = await response.json();
        availableAssignees = {
            named: Array.isArray(data.named) ? data.named : [],
            hasUnassigned: Boolean(data.has_unassigned),
        };
    } catch (err) {
        console.warn('Failed to load assignees:', err);
        // Keep previous cache; dropdown still works with last-known data.
    }
}

function handleAllVaultCheckbox() {
    const dropdown = document.getElementById('vault-selector-dropdown');
    const checkboxes = Array.from(dropdown.querySelectorAll('input[type="checkbox"]:not(#vault-cb-all)'));
    const allChecked = checkboxes.every(cb => cb.checked);

    if (allChecked) {
        // Uncheck all
        checkboxes.forEach(cb => {
            cb.checked = false;
            cb.closest('.vault-selector-item').classList.remove('checked');
        });
        const allCb = document.getElementById('vault-cb-all');
        allCb.checked = false;
        allCb.closest('.vault-selector-item').classList.remove('checked');
    } else {
        // Check all
        checkboxes.forEach(cb => {
            cb.checked = true;
            cb.closest('.vault-selector-item').classList.add('checked');
        });
        const allCb = document.getElementById('vault-cb-all');
        allCb.checked = true;
        allCb.closest('.vault-selector-item').classList.add('checked');
    }

    currentVault = null;
    saveVaultSelection();
    updateVaultLabel();
    updateURL();
    loadAssignees();  // refresh option set for the newly selected vault(s)
    loadCurrentView();
}

function handleVaultCheckboxChange(e) {
    const dropdown = document.getElementById('vault-selector-dropdown');
    const checkboxes = Array.from(dropdown.querySelectorAll('input[type="checkbox"]:not(#vault-cb-all)'));

    // Update checked styling
    e.target.closest('.vault-selector-item').classList.toggle('checked', e.target.checked);

    const checkedVaults = checkboxes.filter(cb => cb.checked).map(cb => cb.value);

    const allCb = document.getElementById('vault-cb-all');
    if (checkedVaults.length === 0) {
        // None checked → empty state (treated as "all" for API)
        allCb.checked = false;
        allCb.closest('.vault-selector-item').classList.remove('checked');
        currentVault = null;
    } else if (checkedVaults.length === checkboxes.length) {
        // All checked → treat as "all"
        allCb.checked = true;
        allCb.closest('.vault-selector-item').classList.add('checked');
        currentVault = null;
    } else {
        allCb.checked = false;
        allCb.closest('.vault-selector-item').classList.remove('checked');
        currentVault = checkedVaults.length === 1 ? checkedVaults[0] : checkedVaults;
    }

    saveVaultSelection();
    updateVaultLabel();
    updateURL();
    loadAssignees();
    loadCurrentView();
}

function saveVaultSelection() {
    if (currentVault === null) {
        localStorage.removeItem('selectedVaults');
    } else if (Array.isArray(currentVault)) {
        localStorage.setItem('selectedVaults', JSON.stringify(currentVault));
    } else {
        localStorage.setItem('selectedVaults', JSON.stringify([currentVault]));
    }
}

function updateVaultLabel() {
    const label = document.getElementById('vault-selector-label');
    if (!label) return;

    if (currentVault === null) {
        label.textContent = 'All';
    } else if (Array.isArray(currentVault)) {
        const text = currentVault.join(', ');
        label.textContent = text.length > 20 ? text.slice(0, 20) + '...' : text;
    } else {
        const text = currentVault;
        label.textContent = text.length > 20 ? text.slice(0, 20) + '...' : text;
    }
}

function filterByAssignee(assignee) {
    // Toggle membership in the array - if already present, remove; otherwise add
    const idx = currentAssignees.indexOf(assignee);
    if (idx === -1) {
        currentAssignees.push(assignee);
    } else {
        currentAssignees.splice(idx, 1);
    }

    // Update URL
    updateURL();

    // Reload the active view (so the operator on Goals does not get clobbered
    // by a tasks re-fetch).
    loadCurrentView();
}

async function assignToMe(taskId, vault) {
    try {
        const response = await fetch(
            `/api/tasks/${encodeURIComponent(taskId)}/assign-to-me?vault=${encodeURIComponent(vault)}`,
            { method: 'PATCH' }
        );
        if (!response.ok) {
            const detail = await parseErrorResponse(response);
            console.error(`Assign to me failed: ${response.status} ${detail}`);
            showToast(detail, true);
            return;
        }
        await loadCurrentView();
    } catch (err) {
        console.error('Assign to me network error:', err);
        showToast(err.message || 'Network error — see console.', true);
    }
}

async function assignGoalToMe(goalId, vault) {
    try {
        const response = await fetch(
            `/api/goals/${encodeURIComponent(goalId)}/assign-to-me?vault=${encodeURIComponent(vault)}`,
            { method: 'PATCH' }
        );
        if (!response.ok) {
            const detail = await parseErrorResponse(response);
            console.error(`Assign goal to me failed: ${response.status} ${detail}`);
            showToast(detail, true);
            return;
        }
        await loadCurrentView();
    } catch (err) {
        console.error('Assign goal to me network error:', err);
        showToast(err.message || 'Network error — see console.', true);
    }
}

function updateURL() {
    const params = new URLSearchParams();

    // Add vault parameter(s)
    if (currentVault === null) {
        // No vault param = all vaults
    } else if (Array.isArray(currentVault)) {
        currentVault.forEach(v => params.append('vault', v));
    } else {
        params.set('vault', currentVault);
    }

    // Add assignee parameter(s) — emit one repeated param per value (preserves empty-token "unassigned" marker)
    currentAssignees.forEach(a => params.append('assignee', a));

    // Add status parameter(s) — always emit explicitly, even when selection equals the default.
    // Omitted only when currentStatuses is empty (all deselected).
    currentStatuses.forEach(s => params.append('status', s));

    // Add goal parameter(s) — emit one repeated param per value
    currentGoals.forEach(g => params.append('goal', g));

    // Add view parameter — always emit explicitly (so reload lands in the same view)
    params.set('view', currentView);

    // Add sort parameter — always emit explicitly (so reload lands in the same order)
    params.set('sort', currentSort);


    // Update URL without reload
    const newURL = params.toString() ? `?${params.toString()}` : window.location.pathname;
    window.history.replaceState({}, '', newURL);
}

function setupDragAndDrop() {
    // Add drop handlers to all columns
    const columns = document.querySelectorAll('.cards');
    columns.forEach(column => {
        column.addEventListener('dragover', handleDragOver);
        column.addEventListener('drop', handleDrop);
        column.addEventListener('dragleave', handleDragLeave);
    });
}

function handleDragOver(e) {
    e.preventDefault();
    e.currentTarget.classList.add('drag-over');
}

function handleDragLeave(e) {
    e.currentTarget.classList.remove('drag-over');
}

async function handleDrop(e) {
    e.preventDefault();
    e.currentTarget.classList.remove('drag-over');

    const itemId = e.dataTransfer.getData('text/plain');
    const targetKey = e.currentTarget.id.replace('cards-', '');

    // Detect goal vs task by cache lookup. Tasks-view drops resolve via
    // tasksCache (column id is the phase); Goals-view drops resolve via
    // goalsCache (column id is the status).
    const task = tasksCache[itemId];
    const goal = goalsCache[itemId];

    if (task) {
        // Dropping into the Done column is a completed-targeting close-out —
        // reason-free (abort-only contract); the PATCH body carries no
        // close-out fields.
        try {
            const body = { phase: targetKey };
            const response = await fetch(`/api/tasks/${itemId}/phase?vault=${encodeURIComponent(task.vault)}`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(body),
            });
            if (!response.ok) throw new Error(await parseErrorResponse(response));
            await loadCurrentView();
        } catch (error) {
            console.error('Failed to update task phase:', error);
            showToast(error.message, true);
        }
        return;
    }

    if (goal) {
        // Dropping into the Completed column is a completed-targeting close-out —
        // reason-free (abort-only contract); the PATCH body carries no
        // close-out fields.
        try {
            const body = { status: targetKey };
            const response = await fetch(`/api/goals/${encodeURIComponent(itemId)}/status?vault=${encodeURIComponent(goal.vault)}`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(body),
            });
            if (!response.ok) throw new Error(await parseErrorResponse(response));
            await loadCurrentView();
        } catch (error) {
            console.error('Failed to update goal status:', error);
            showToast(error.message, true);
        }
        return;
    }

    showToast('Item not found', true);
}

async function loadTasks() {
    try {
        // Build API URL
        const params = new URLSearchParams();

        // Add vault parameter(s)
        if (currentVault === null) {
            // No vault param = all vaults
        } else if (Array.isArray(currentVault)) {
            currentVault.forEach(v => params.append('vault', v));
        } else {
            params.set('vault', currentVault);
        }

        // Add other filters — include completed so recently-completed tasks appear in Done lane
        currentStatuses.forEach(s => params.append('status', s));
        params.set('phase', 'todo,planning,in_progress,execution,ai_review,human_review,done');

        // Add assignee parameter(s) — pass through every value the user selected
        currentAssignees.forEach(a => params.append('assignee', a));

        // Add goal parameter(s) — pass through every value from the URL
        currentGoals.forEach(g => params.append('goal', g));

        // Upcoming-window cutoff (hours ahead) — 0 hides all deferred tasks
        params.set('upcoming_hours', String(upcomingHours));

        // Fetch tasks
        const response = await fetch(`/api/tasks?${params.toString()}`);
        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }

        const tasks = await response.json();

        // Cache tasks for quick lookup
        tasksCache = {};
        tasks.forEach(task => {
            tasksCache[task.id] = task;
        });

        renderTasks();

        // Refresh the assignee dropdown so options reflect the freshly loaded data.
        renderAssigneeDropdown();
        updateAssigneeLabel();

    } catch (error) {
        console.error('Failed to load tasks:', error);
        showToast(error.message, true);
    }
}

// Render the board from the tasks cache, ordered by the active sort key.
// Split out of loadTasks so a sort change can re-render without refetching.
function renderTasks() {
    const tasks = Object.values(tasksCache);

    // Clear existing cards
    ['todo', 'planning', 'execution', 'ai_review', 'human_review', 'done'].forEach(phase => {
        const container = document.getElementById(`cards-${phase}`);
        if (container) {
            container.innerHTML = '';
        }
    });

    // Sort tasks by the active sort key (see sortBoardItems). The default
    // order is urgency tier first (0=overdue, 1=due-today, 2=scheduled, 3=none),
    // then by priority within each tier (high=1, medium=2, low=3, null=999).
    tasks.sort((a, b) => sortBoardItems(a, b, 'task'));

    // Split tasks into buckets that fix column ordering: active → upcoming → hold,
    // with recently_completed pinned to the bottom of the Done lane.
    // Hold takes precedence over upcoming so a hold task with a future defer_date
    // still sinks to the bottom (parked/blocked, not actionable now).
    const recentlyCompletedTasks = tasks.filter(t => t.recently_completed);
    const holdTasks = tasks.filter(t => !t.recently_completed && t.status === 'hold');
    const upcomingTasks = tasks.filter(
        t => !t.recently_completed && t.status !== 'hold' && t.upcoming
    );
    const activeTasks = tasks.filter(
        t => !t.recently_completed && t.status !== 'hold' && !t.upcoming
    );

    // Populate cards: active first, then upcoming, then hold at the bottom;
    // recently-completed always at bottom of done.
    const validPhases = ['todo', 'planning', 'execution', 'ai_review', 'human_review', 'done'];
    [...activeTasks, ...upcomingTasks, ...holdTasks].forEach(task => {
        let containerId;
        if (currentGroupBy === 'status') {
            // Status-mode for tasks: status is the column discriminator.
            // Tasks without a matching status land in the first column
            // (in_progress) as a fallback — tasks should always have a
            // status, but defensiveness costs nothing here.
            const taskStatus = task.status || 'in_progress';
            containerId = `cards-${taskStatus}`;
        } else {
            // phase-mode: existing behavior — in_progress → execution alias.
            const displayPhase = task.phase === 'in_progress' ? 'execution' : task.phase;
            const phase = displayPhase && validPhases.includes(displayPhase) ? displayPhase : 'todo';
            containerId = `cards-${phase}`;
        }
        const container = document.getElementById(containerId);
        if (container) {
            const card = createTaskCard(task);
            container.appendChild(card);
        }
    });
    // Recently completed always go to done lane at the very bottom
    const doneContainer = document.getElementById('cards-done');
    if (doneContainer) {
        recentlyCompletedTasks.forEach(task => {
            doneContainer.appendChild(createTaskCard(task));
        });
    }
}

async function loadGoals() {
    try {
        const params = new URLSearchParams();
        if (currentVault === null) {
            // No vault param = all vaults
        } else if (Array.isArray(currentVault)) {
            currentVault.forEach(v => params.append('vault', v));
        } else {
            params.set('vault', currentVault);
        }
        // Mirror task query params the user has set
        currentStatuses.forEach(s => params.append('status', s));
        currentAssignees.forEach(a => params.append('assignee', a));

        // Upcoming-window cutoff (hours ahead) — 0 hides all deferred goals
        params.set('upcoming_hours', String(upcomingHours));

        const response = await fetch(`/api/goals?${params.toString()}`);
        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }

        const goals = await response.json();
        goalsCache = {};
        goals.forEach(goal => {
            goalsCache[goal.id] = goal;
        });

        renderGoals();
    } catch (error) {
        console.error('Failed to load goals:', error);
        showToast(error.message, true);
    }
}

// Render the goals board from the goals cache, ordered by the active sort key.
// Split out of loadGoals so a sort change can re-render without refetching.
function renderGoals() {
    const goals = Object.values(goalsCache);

    // Clear all cards containers that match the active grouping's columns.
    const containerIds = currentGroupBy === 'status'
        ? ['in_progress', 'next', 'backlog', 'completed', 'hold', 'aborted']
        : ['todo', 'planning', 'execution', 'ai_review', 'human_review', 'done', 'unknown'];
    containerIds.forEach(id => {
        const container = document.getElementById(`cards-${id}`);
        if (container) container.innerHTML = '';
    });

    // Sort goals by the active sort key (see sortBoardItems). The default
    // order is priority (1=highest, null=999=last), then alphabetically
    // by id within same priority, so cards in each column read top-down
    // from most-important to least.
    goals.sort((a, b) => sortBoardItems(a, b, 'goal'));

    // Split goals into buckets so upcoming (deferred, within-window) and hold
    // goals sink to the bottom of their column — mirrors the task bucketing
    // (active → upcoming → hold). Hold takes precedence over upcoming so a
    // held goal with a future defer_date still sinks.
    const holdGoals = goals.filter(g => g.status === 'hold');
    const upcomingGoals = goals.filter(g => g.status !== 'hold' && g.upcoming);
    const activeGoals = goals.filter(g => g.status !== 'hold' && !g.upcoming);

    [...activeGoals, ...upcomingGoals, ...holdGoals].forEach(goal => {
        let containerId;
        if (currentGroupBy === 'status') {
            // Status-mode for goals: status is the column discriminator
            // (same as tasks in status-mode). Goals should always have
            // a status; missing status → 'in_progress' as fallback.
            const goalStatus = goal.status || 'in_progress';
            containerId = `cards-${goalStatus}`;
        } else {
            // phase-mode for goals: goals don't have a phase field,
            // so they all land in the "—" column.
            containerId = 'cards-unknown';
        }
        const container = document.getElementById(containerId);
        if (container) {
            const card = createGoalCard(goal);
            container.appendChild(card);
        }
    });
}

async function loadCurrentView() {
    // Single in-flight fetch for the active view only.
    // On initial load with ?view=goals, this is the ONLY fetch issued —
    // /api/tasks is NOT called. (Spec AC#7 evidence:
    // performance.getEntriesByType('resource') must not contain /api/tasks.)
    if (currentView === 'goals') {
        await loadGoals();
    } else {
        await loadTasks();
    }
}

function extractJiraIssue(title) {
    // Detect Jira issue key pattern: PROJECT-NUMBER
    const jiraKeyPattern = /\b([A-Z]+)-(\d+)\b/;
    const match = title.match(jiraKeyPattern);

    if (!match) {
        return { title: title, issueKey: null, issueUrl: null };
    }

    const issueKey = match[0];
    const project = match[1];

    // Map project keys to Atlassian domains
    const projectDomains = {
        'BRO': 'seibertgroup.atlassian.net',
        'TRADE': 'borbe.atlassian.net'
    };

    const domain = projectDomains[project];
    const issueUrl = domain ? `https://${domain}/browse/${issueKey}` : null;

    // Remove issue key from title
    const cleanTitle = title.replace(jiraKeyPattern, '').trim();

    return { title: cleanTitle, issueKey, issueUrl };
}

// Shared Start / Resume / Starting button for task and goal cards. Written once;
// both card renderers call it. hasSession/isStarting gate the three labels off
// claude_session_id and the durable claude_session_started flag (plus the optimistic
// per-tab starting set), mirroring the pre-collapse per-kind blocks exactly.
// Elapsed time for the "Starting…" badge, from the claude_session_started marker.
// The marker is an ISO-8601 launch instant; legacy markers are the literal "true"
// and carry no age, so they render the bare label. Returns '' when unknown.
function startingElapsedLabel(marker) {
    if (!marker || marker === 'true') return '';
    const started = Date.parse(marker);
    if (Number.isNaN(started)) return '';
    const secs = Math.max(0, Math.floor((Date.now() - started) / 1000));
    const mins = Math.floor(secs / 60);
    return ` ${mins}:${String(secs % 60).padStart(2, '0')}`;
}

function sessionButtonHtml(kind, item) {
    const startingSet = kind === 'goal' ? startingGoals : startingTasks;
    const hasSession = item.claude_session_id;
    const isStarting = !hasSession && (!!item.claude_session_started || startingSet.has(item.id));
    let buttonLabel, buttonClass, buttonDisabled, buttonTitle = '';
    if (isStarting) {
        buttonLabel = `⏳ Starting...${startingElapsedLabel(item.claude_session_started)}`;
        buttonClass = 'start-btn';
        buttonDisabled = true;
    } else if (item.session_state === 'live') {
        // Live session — running now. A plain resume is flock-refused (vault-cli
        // path) or corrupting (launcher path), so offer take-over instead: the
        // badge itself is the affordance (discreet — no extra button). Clicking
        // it opens the confirm dialog, which ends the running turn (SIGTERM via
        // the ps --resume match), then Resume works normally. In-flight work is
        // lost — that is the accepted trade-off, stated in the confirm dialog.
        return `<span class="live-badge" role="button" tabindex="0" onclick="takeOverSession('${kind}', '${item.id}')" title="Session is live — click to take over and resume (ends the running turn; in-flight work is lost)">● Live</span>`;
    } else if (hasSession) {
        if (item.session_state === 'indeterminate') {
            // Session id present but no transcript found — cannot prove it dead
            // (manual terminal /resume in another cwd, cloud/container session).
            // Mark it rather than offering a Resume we cannot honor.
            buttonLabel = '▶ Resume';
            buttonClass = 'resume-btn indeterminate';
            buttonDisabled = true;
            buttonTitle = ' title="Session state unknown — may be open elsewhere; resume disabled"';
        } else {
            buttonLabel = '▶ Resume';
            buttonClass = 'resume-btn';
            buttonDisabled = false;
        }
    } else {
        buttonLabel = '▶ Start';
        buttonClass = 'start-btn';
        buttonDisabled = false;
    }
    return `<button class="${buttonClass}" onclick="runSession('${kind}', '${item.id}')"${buttonDisabled ? ' disabled' : ''}${buttonTitle}>${buttonLabel}</button>`;
}

// Shared card body: menu button + title block + footer skeleton. The kind-specific
// footer-left content (badges/assignee), card classes, dataset, urgency/on-hold, and
// drag wiring stay in the thin createTaskCard/createGoalCard wrappers.
// Age of the last activity on a card, as a single largest unit with no decimals.
// The server resolves the timestamp (newer of task file mtime and Claude session
// transcript mtime); this only formats it. Glanceable staleness, not precision —
// so "2h" rather than "2h 14m".
function formatActivityAge(iso) {
    if (!iso) return '';
    const then = new Date(iso);
    if (isNaN(then.getTime())) return '';

    const seconds = Math.floor((Date.now() - then.getTime()) / 1000);
    if (seconds < 60) return '<1m';

    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return `${minutes}m`;

    const hours = Math.floor(minutes / 60);
    if (hours < 24) return `${hours}h`;

    const days = Math.floor(hours / 24);
    if (days < 7) return `${days}d`;

    return `${Math.floor(days / 7)}w`;
}

function activityAgeHtml(activityDate) {
    const age = formatActivityAge(activityDate);
    if (!age) return '';
    return `<span class="activity-age" title="Last activity: ${escapeHtml(activityDate)}">${escapeHtml(age)}</span>`;
}

function cardShellHtml(kind, id, obsidianUrl, title, footerLeftHtml, startButtonHtml) {
    const menuButton = '<button class="menu-btn" onclick="showMenu(event, \'' + kind + '\', \'' + id + '\')">⋮</button>';
    return `
        ${menuButton}
        <div class="card-content">
            <h3 class="task-title">
                <a href="${obsidianUrl}" class="task-title-link" title="Open in Obsidian">
                    ${escapeHtml(title)}
                    <span class="obsidian-icon">↗</span>
                </a>
            </h3>
        </div>
        <div class="card-footer">
            <div class="card-footer-left">${footerLeftHtml}</div>
            <div class="card-actions">${startButtonHtml}</div>
        </div>
    `;
}

function createTaskCard(task) {
    const card = document.createElement('div');
    card.className = 'task-card';
    card.draggable = true;
    card.dataset.taskId = task.id;

    // Apply urgency border class
    const tier = getUrgencyTier(task);
    if (tier === 0) card.classList.add('urgency-overdue');
    else if (tier === 1) card.classList.add('urgency-today');
    else if (tier === 2) card.classList.add('urgency-scheduled');
    // tier === 3: no class, default appearance

    if (task.upcoming) card.classList.add('upcoming');
    if (task.recently_completed) card.classList.add('recently-completed');
    if (task.status === 'hold') card.classList.add('on-hold');

    // Drag handlers
    card.addEventListener('dragstart', (e) => {
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', task.id);
        card.classList.add('dragging');
    });

    card.addEventListener('dragend', () => {
        card.classList.remove('dragging');
    });

    // Extract Jira issue info
    const { title, issueKey, issueUrl } = extractJiraIssue(task.title);

    // On-hold badge (if status is hold) — signals the task is parked/blocked
    const holdBadge = task.status === 'hold'
        ? '<span class="hold-badge" title="On hold — blocked, not actively worked">⏸ HOLD</span>'
        : '';

    // Jira issue badge (if present)
    const jiraBadge = issueKey && issueUrl
        ? `<a href="${issueUrl}" class="jira-badge" target="_blank" title="Open in Jira">
             <span class="jira-icon">🔖</span><span>${escapeHtml(issueKey)}</span>
           </a>`
        : '';

    // Assignee badge (if present) - clickable to filter
    const isActiveFilter = currentAssignees.includes(task.assignee);
    const assigneeBadge = task.assignee
        ? `<span class="assignee-badge clickable ${isActiveFilter ? 'active' : ''}" onclick="filterByAssignee('${escapeHtml(task.assignee)}')" title="${isActiveFilter ? 'Clear filter' : 'Filter by ' + escapeHtml(task.assignee)}">
             <span class="assignee-icon">👤</span><span>${escapeHtml(task.assignee)}</span>
           </span>`
        : `<a class="assign-to-me-link" onclick="assignToMe('${escapeHtml(task.id)}', '${escapeHtml(task.vault)}')" title="Assign this task to me">+ Assign to me</a>`;

    const startButton = sessionButtonHtml('task', task);
    const footerLeft = `
        ${holdBadge}
        ${jiraBadge}
        ${assigneeBadge}
        ${task.priority ? `<span class="priority-chip" title="Priority ${escapeHtml(String(task.priority))}">P${escapeHtml(String(task.priority))}</span>` : ''}
        ${activityAgeHtml(task.activity_date)}
    `;
    card.innerHTML = cardShellHtml('task', task.id, task.obsidian_url, title, footerLeft, startButton);
    return card;
}

function createGoalCard(goal) {
    const card = document.createElement('div');
    card.className = 'task-card goal-card';
    card.dataset.goalId = goal.id;
    card.dataset.kind = 'goal';
    card.draggable = true;
    if (goal.status === 'hold') card.classList.add('on-hold');
    if (goal.upcoming) card.classList.add('upcoming');

    // Drag handlers — mirror createTaskCard, set dataTransfer to the goal id
    // so handleDrop can detect goal-vs-task via cache lookup (goalsCache hit
    // → goal status update; tasksCache hit → task phase update).
    card.addEventListener('dragstart', (e) => {
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', goal.id);
        card.classList.add('dragging');
    });
    card.addEventListener('dragend', () => {
        card.classList.remove('dragging');
    });

    const { title, issueKey, issueUrl } = extractJiraIssue(goal.title);

    const holdBadge = goal.status === 'hold'
        ? '<span class="hold-badge" title="On hold — paused, not actively worked">⏸ HOLD</span>'
        : '';

    // Jira issue badge (if present)
    const jiraBadge = issueKey && issueUrl
        ? `<a href="${issueUrl}" class="jira-badge" target="_blank" title="Open in Jira">
             <span class="jira-icon">🔖</span><span>${escapeHtml(issueKey)}</span>
           </a>`
        : '';

    const startButton = sessionButtonHtml('goal', goal);
    const menuButton = '<button class="menu-btn" onclick="showMenu(event, \'goal\', \'' + goal.id + '\')">⋮</button>';
    const footerLeft = `
        ${holdBadge}
        ${jiraBadge}
        ${goal.assignee
            ? `<span class="assignee-badge"><span class="assignee-icon">👤</span><span>${escapeHtml(goal.assignee)}</span></span>`
            : `<a class="assign-to-me-link" onclick="assignGoalToMe('${escapeHtml(goal.id)}', '${escapeHtml(goal.vault)}')" title="Assign this goal to me">+ Assign to me</a>`}
        ${goal.priority ? `<span class="priority-chip" title="Priority ${escapeHtml(String(goal.priority))}">P${escapeHtml(String(goal.priority))}</span>` : ''}
        ${activityAgeHtml(goal.activity_date)}
    `;
    card.innerHTML = `
        ${menuButton}
        <div class="card-content">
            <h3 class="task-title">
                <a href="${goal.obsidian_url}" class="task-title-link" title="Open in Obsidian">
                    ${escapeHtml(title)}
                    <span class="obsidian-icon">↗</span>
                </a>
            </h3>
        </div>
        <div class="card-footer">
            <div class="card-footer-left">${footerLeft}</div>
            <div class="card-actions">${startButton}</div>
        </div>
    `;
    return card;
}

async function runSession(kind, id) {
    // Arg-injection guard on the merged path (spec AC + Security): reject ids
    // beginning with '-' before any fetch, covering BOTH kinds at once. Mirrors
    // the backend guard in api/tasks.py.
    if (typeof id === 'string' && id.startsWith('-')) {
        showToast('Invalid id', true);
        return;
    }

    const base = kind === 'goal' ? 'goals' : 'tasks';
    const cache = kind === 'goal' ? goalsCache : tasksCache;
    const startingSet = kind === 'goal' ? startingGoals : startingTasks;

    const item = cache[id];
    if (!item) {
        showToast(kind === 'goal' ? 'Goal not found in cache' : 'Task not found in cache', true);
        return;
    }

    const button = event.target;
    const originalText = button.textContent;

    try {
        button.textContent = '⏳ Loading...';
        button.disabled = true;

        // Resume short-circuit: an item with a session opens the modal directly.
        if (item.claude_session_id) {
            const vaultsResponse = await fetch('/api/vaults');
            const vaults = await vaultsResponse.json();
            const vaultConfig = vaults.find(v => v.name === item.vault);
            if (!vaultConfig) {
                throw new Error('Vault not found');
            }
            const command = `${vaultConfig.claude_script} --resume ${item.claude_session_id}`;
            showModal(item.claude_session_id, command, vaultConfig.vault_path, item.title);
            button.textContent = originalText;
            button.disabled = false;
            return;
        }

        // Task-only "Creating session…" loading modal, preserved verbatim from runTask.
        // Goals never had this overlay; the kind gate keeps both behaviors unchanged.
        let userDismissed = false;
        let loadingModal = null;
        let closeBtn = null;
        let closeHandler = null;
        if (kind === 'task') {
            loadingModal = document.getElementById('loading-modal');
            loadingModal.classList.remove('hidden');
            closeBtn = document.getElementById('close-loading-btn');
            closeHandler = () => {
                userDismissed = true;
                loadingModal.classList.add('hidden');
                closeBtn.removeEventListener('click', closeHandler);
                renderTasks();
            };
            closeBtn.addEventListener('click', closeHandler);
        }

        startingSet.add(id);
        button.textContent = '⏳ Starting...';
        const response = await fetch(
            `/api/${base}/${encodeURIComponent(id)}/run?vault=${encodeURIComponent(item.vault)}`,
            { method: 'POST' }
        );
        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }

        const data = await response.json();

        if (kind === 'task') {
            closeBtn.removeEventListener('click', closeHandler);
            loadingModal.classList.add('hidden');
        }
        startingSet.delete(id);
        item.claude_session_id = data.session_id;

        if (!userDismissed) {
            showModal(data.session_id, data.command, data.working_dir, data.task_title);
        }

        button.textContent = '▶ Resume';
        button.className = 'resume-btn';
        button.disabled = false;
    } catch (error) {
        startingSet.delete(id);
        console.error(`Failed to run ${kind}:`, error);
        if (kind === 'task') {
            const loadingModal = document.getElementById('loading-modal');
            loadingModal.classList.add('hidden');
            await new Promise(r => requestAnimationFrame(r));  // ensure modal hides before toast renders
        }
        showToast(error.message, true);
        if (event && event.target) {
            event.target.textContent = '▶ Start';
            event.target.disabled = false;
        }
    }
}

// Take-over confirm dialog: asks the operator to end the running turn before
// resuming a live session. Resolves true on Confirm, false on Cancel — mirror
// of askCloseOut's promise shape, minus the free-text reason fields.
function askTakeOver() {
    const modal = document.getElementById('takeover-modal');
    const confirmBtn = document.getElementById('takeover-confirm-btn');
    const cancelBtn = document.getElementById('takeover-cancel-btn');

    let resolvePromise;
    const teardown = () => {
        confirmBtn.removeEventListener('click', onConfirm);
        cancelBtn.removeEventListener('click', onCancel);
    };
    const onConfirm = () => {
        teardown();
        modal.classList.add('hidden');
        resolvePromise(true);
    };
    const onCancel = () => {
        teardown();
        modal.classList.add('hidden');
        resolvePromise(false);
    };

    confirmBtn.addEventListener('click', onConfirm);
    cancelBtn.addEventListener('click', onCancel);

    modal.classList.remove('hidden');

    return new Promise((resolve) => {
        resolvePromise = resolve;
    });
}

// Take over a live session: confirm, then POST to the backend which SIGTERMs the
// matched `claude --resume <uuid>` process (releasing the flock) and returns the
// resume command — shown in the session modal so the operator can resume.
async function takeOverSession(kind, id) {
    // Arg-injection guard (mirrors runSession).
    if (typeof id === 'string' && id.startsWith('-')) {
        showToast('Invalid id', true);
        return;
    }

    const base = kind === 'goal' ? 'goals' : 'tasks';
    const cache = kind === 'goal' ? goalsCache : tasksCache;
    const item = cache[id];
    if (!item) {
        showToast(kind === 'goal' ? 'Goal not found in cache' : 'Task not found in cache', true);
        return;
    }

    // Destructive-action gate (SC2): cancel performs no action.
    const confirmed = await askTakeOver();
    if (!confirmed) return;

    try {
        const response = await fetch(
            `/api/${base}/${encodeURIComponent(id)}/take-over?vault=${encodeURIComponent(item.vault)}`,
            { method: 'POST' }
        );
        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }

        const data = await response.json();
        showModal(data.session_id, data.command, data.working_dir, data.task_title);
        await loadCurrentView();
    } catch (error) {
        console.error(`Failed to take over ${kind}:`, error);
        showToast(error.message, true);
    }
}

function showModal(sessionId, command, workingDir, taskTitle = null, executedCommand = null, success = null, error = null) {
    document.getElementById('session-id').textContent = sessionId;
    document.getElementById('handoff-command').textContent = command;

    // Update task title if provided
    if (taskTitle) {
        document.getElementById('task-title').textContent = taskTitle;
    } else {
        document.getElementById('task-title').textContent = 'Unknown';
    }

    // Update executed command if provided
    if (executedCommand) {
        document.getElementById('executed-command').textContent = executedCommand;
    } else {
        document.getElementById('executed-command').textContent = '/work-on-task';
    }

    // Show success/failure status
    const statusMessage = document.getElementById('status-message');
    if (success === true) {
        statusMessage.textContent = '✓ Command completed successfully';
        statusMessage.style.backgroundColor = '#d4edda';
        statusMessage.style.color = '#155724';
        statusMessage.style.display = 'block';
    } else if (success === false) {
        statusMessage.textContent = '✗ Command failed' + (error ? ': ' + error : '');
        statusMessage.style.backgroundColor = '#f8d7da';
        statusMessage.style.color = '#721c24';
        statusMessage.style.display = 'block';
    } else {
        statusMessage.style.display = 'none';
    }

    document.getElementById('session-modal').classList.remove('hidden');
}

function closeModal() {
    document.getElementById('session-modal').classList.add('hidden');
}

// Open the close-out reason modal; resolve with { reason, gate_successor } on
// Confirm, or null when the operator cancels. Confirm is disabled while the
// reason is empty/whitespace-only, so a blank reason can never be submitted.
function askCloseOut(kind, verb) {
    const modal = document.getElementById('reason-modal');
    const title = document.getElementById('reason-title');
    const riskPrompt = document.getElementById('reason-risk-prompt');
    const reasonInput = document.getElementById('reason-input');
    const gateInput = document.getElementById('gate-successor-input');
    const confirmBtn = document.getElementById('reason-confirm-btn');
    const cancelBtn = document.getElementById('reason-cancel-btn');

    const kindLabel = kind === 'goal' ? 'Goal' : 'Task';
    const verbLabel = verb === 'abort' ? 'Abort' : 'Complete';
    title.textContent = `${verbLabel} ${kindLabel}`;
    riskPrompt.textContent = kind === 'goal'
        ? "Does this goal own a trigger, gate, threshold or recurring check? If so, name where it moves (gate successor), or 'none'."
        : "Does this task own a trigger, gate, threshold or recurring check? If so, name where it moves (gate successor), or 'none'.";

    reasonInput.value = '';
    gateInput.value = '';
    confirmBtn.disabled = true;

    let resolvePromise;
    const teardown = () => {
        reasonInput.removeEventListener('input', onReasonInput);
        confirmBtn.removeEventListener('click', onConfirm);
        cancelBtn.removeEventListener('click', onCancel);
    };
    const onReasonInput = () => {
        confirmBtn.disabled = !reasonInput.value.trim();
    };
    const onConfirm = () => {
        const reason = reasonInput.value.trim();
        if (!reason) {
            return; // guard — Confirm is disabled while blank; never submit a blank reason
        }
        const result = { reason, gate_successor: gateInput.value.trim() || 'none' };
        teardown();
        modal.classList.add('hidden');
        resolvePromise(result);
    };
    const onCancel = () => {
        teardown();
        modal.classList.add('hidden');
        resolvePromise(null);
    };

    reasonInput.addEventListener('input', onReasonInput);
    confirmBtn.addEventListener('click', onConfirm);
    cancelBtn.addEventListener('click', onCancel);

    modal.classList.remove('hidden');
    reasonInput.focus();

    return new Promise((resolve) => {
        resolvePromise = resolve;
    });
}

function updateModal(sessionId, command, workingDir, taskTitle = null, executedCommand = null, success = null, error = null) {
    // Only update if modal is already visible
    const modal = document.getElementById('session-modal');
    if (modal.classList.contains('hidden')) {
        return;
    }

    document.getElementById('session-id').textContent = sessionId;
    document.getElementById('handoff-command').textContent = command;

    if (taskTitle) {
        document.getElementById('task-title').textContent = taskTitle;
    } else {
        document.getElementById('task-title').textContent = 'Unknown';
    }

    if (executedCommand) {
        document.getElementById('executed-command').textContent = executedCommand;
    } else {
        document.getElementById('executed-command').textContent = '/work-on-task';
    }

    const statusMessage = document.getElementById('status-message');
    if (success === true) {
        statusMessage.textContent = '✓ Command completed successfully';
        statusMessage.style.backgroundColor = '#d4edda';
        statusMessage.style.color = '#155724';
        statusMessage.style.display = 'block';
    } else if (success === false) {
        statusMessage.textContent = '✗ Command failed' + (error ? ': ' + error : '');
        statusMessage.style.backgroundColor = '#f8d7da';
        statusMessage.style.color = '#721c24';
        statusMessage.style.display = 'block';
    } else {
        statusMessage.style.display = 'none';
    }
}

async function copyCommand() {
    const command = document.getElementById('handoff-command').textContent;

    try {
        await navigator.clipboard.writeText(command);

        // Show feedback
        const button = document.getElementById('copy-btn');
        const originalText = button.textContent;
        button.textContent = '✓ Copied!';

        setTimeout(() => {
            button.textContent = originalText;
        }, 2000);
    } catch (error) {
        console.error('Failed to copy:', error);
        showToast('Failed to copy to clipboard', true);
    }
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

// Sort cards within a column by the active sort key (currentSort, driven by
// the header sort-select). 'default' keeps each view's existing ordering
// (tasks: urgency tier → priority; goals: priority → id). 'priority' is
// priority-only (highest first). 'modified' is most-recent-activity first via
// activity_date — the small age number each card shows — with items lacking a
// date sinking to the bottom. Ties fall through to the default ordering so
// cards don't jitter between renders.
function sortBoardItems(a, b, kind) {
    if (currentSort === 'priority') {
        return normalizePriority(a.priority) - normalizePriority(b.priority);
    }
    if (currentSort === 'modified') {
        const ma = activityDateMs(a);
        const mb = activityDateMs(b);
        if (ma !== mb) return mb - ma; // descending; -Infinity (missing date) sinks
    }
    return defaultSortCompare(a, b, kind);
}

// Millisecond epoch of activity_date, or -Infinity when absent so the item
// sinks under the descending 'modified' order.
function activityDateMs(item) {
    return item.activity_date ? new Date(item.activity_date).getTime() : -Infinity;
}

// The ordering each view used before the sort control existed — preserved
// verbatim as the 'default' branch so nothing changes when no sort is chosen.
function defaultSortCompare(a, b, kind) {
    if (kind === 'goal') {
        // Goals carry no due/planned dates, so there is no urgency tier:
        // priority first, then id for a stable tiebreak.
        const pa = normalizePriority(a.priority);
        const pb = normalizePriority(b.priority);
        if (pa !== pb) return pa - pb;
        return (a.id || '').localeCompare(b.id || '');
    }
    // Tasks: urgency tier (0=overdue, 1=due-today, 2=scheduled, 3=none), then
    // priority within each tier.
    const ta = getUrgencyTier(a);
    const tb = getUrgencyTier(b);
    if (ta !== tb) return ta - tb;
    return normalizePriority(a.priority) - normalizePriority(b.priority);
}

function normalizePriority(priority) {
    // Map priority to numeric value for sorting
    // high=1, medium=2, low=3, unknown/null=999
    if (priority === null || priority === undefined) {
        return 999;
    }

    // Handle string priorities
    if (typeof priority === 'string') {
        const lower = priority.toLowerCase();
        if (lower === 'high' || lower === 'highest') return 1;
        if (lower === 'medium') return 2;
        if (lower === 'low') return 3;
        return 999; // Unknown string
    }

    // Handle numeric priorities (already in correct format)
    if (typeof priority === 'number') {
        return priority;
    }

    return 999; // Fallback
}

/**
 * Returns the urgency tier for a task based on due_date and planned_date.
 * Tier values (lower = more urgent):
 *   0 = overdue (due_date before today, red)
 *   1 = due today (due_date equals today, yellow)
 *   2 = scheduled (planned_date <= today, but not overdue/due-today, blue)
 *   3 = no urgency (no applicable dates)
 */
function getUrgencyTier(task) {
    const today = new Date().toISOString().slice(0, 10); // YYYY-MM-DD

    const dueDate = task.due_date && /^\d{4}-\d{2}-\d{2}$/.test(task.due_date)
        ? task.due_date : null;
    const plannedDate = task.planned_date && /^\d{4}-\d{2}-\d{2}$/.test(task.planned_date)
        ? task.planned_date : null;

    if (dueDate && dueDate < today) return 0;   // overdue
    if (dueDate && dueDate === today) return 1;  // due today
    if (plannedDate && plannedDate <= today) return 2; // scheduled/actionable
    return 3; // no urgency
}

function formatPhase(phase) {
    const phaseNames = {
        'todo': 'Todo',
        'planning': 'Planning',
        'in_progress': 'Execution',
        'execution': 'Execution',
        'ai_review': 'AI Review',
        'human_review': 'Human Review',
        'done': 'Done'
    };
    return phaseNames[phase] || phase;
}

function formatRelativeTime(timestamp) {
    const date = new Date(timestamp);
    const now = new Date();
    const diffMs = now - date;
    const diffSecs = Math.floor(diffMs / 1000);
    const diffMins = Math.floor(diffSecs / 60);
    const diffHours = Math.floor(diffMins / 60);
    const diffDays = Math.floor(diffHours / 24);

    if (diffSecs < 60) {
        return 'just now';
    } else if (diffMins < 60) {
        return `${diffMins}m ago`;
    } else if (diffHours < 24) {
        return `${diffHours}h ago`;
    } else if (diffDays < 7) {
        return `${diffDays}d ago`;
    } else {
        return date.toLocaleDateString();
    }
}

function showMenu(event, kind, id) {
    event.stopPropagation();

    const existingMenu = document.querySelector('.task-menu');
    if (existingMenu) {
        existingMenu.remove();
    }

    const menu = document.createElement('div');
    menu.className = 'task-menu';

    const cache = kind === 'goal' ? goalsCache : tasksCache;
    const item = cache[id];
    const hasSession = item && item.claude_session_id;

    const menuItems = [];
    if (kind === 'goal') {
        if (hasSession) {
            menuItems.push({ label: 'Reset Session', action: 'clear_session' });
        }
        menuItems.push({ label: 'Complete Goal', action: 'complete_goal' });
        menuItems.push({ label: 'Defer Goal', action: 'defer_goal' });
        menuItems.push({ label: 'Abort Goal', action: 'abort_goal' });
        if (item && item.status === 'hold') {
            menuItems.push({ label: 'Resume Goal', action: 'resume_goal' });
        } else {
            menuItems.push({ label: 'Hold Goal', action: 'hold_goal' });
        }
    } else {
        if (hasSession) {
            menuItems.push({ label: 'Clear Session', action: 'clear_session', disabled: false });
        }
        menuItems.push({ label: 'Complete Task', action: 'complete_task', disabled: false });
        menuItems.push({ label: 'Defer Task', action: 'defer_task', disabled: false });
        menuItems.push({ label: 'Abort Task', action: 'abort_task', disabled: false });
        if (item && item.status === 'hold') {
            menuItems.push({ label: 'Resume Task', action: 'resume_task', disabled: false });
        } else {
            menuItems.push({ label: 'Hold Task', action: 'hold_task', disabled: false });
        }
    }

    menuItems.forEach(itemDef => {
        const menuItem = document.createElement('div');
        menuItem.className = 'task-menu-item';
        if (itemDef.disabled) {
            menuItem.classList.add('disabled');
        }
        menuItem.textContent = itemDef.label;
        if (!itemDef.disabled) {
            menuItem.addEventListener('click', () => dispatchMenuAction(kind, id, itemDef.action));
        }
        menu.appendChild(menuItem);
    });

    positionAndBindMenu(menu, event.target);
}

// Position a `.task-menu` next to its trigger button, keep it inside the viewport
// (flip up / clamp horizontally), then bind the click-outside close handler.
// Shared by showMenu.
function positionAndBindMenu(menu, button) {
    const rect = button.getBoundingClientRect();
    menu.style.position = 'fixed';
    menu.style.visibility = 'hidden'; // Hide while measuring

    document.body.appendChild(menu);

    // Measure menu dimensions
    const menuRect = menu.getBoundingClientRect();
    const viewportHeight = window.innerHeight;
    const viewportWidth = window.innerWidth;

    // Calculate vertical position (flip up if doesn't fit below)
    let top = rect.bottom + 5;
    if (top + menuRect.height > viewportHeight) {
        // Open upward
        top = rect.top - menuRect.height - 5;
    }

    // Calculate horizontal position (keep within viewport)
    let left = rect.left - 150;
    if (left < 0) {
        left = 5; // Minimum margin from left edge
    } else if (left + menuRect.width > viewportWidth) {
        left = viewportWidth - menuRect.width - 5;
    }

    menu.style.top = `${top}px`;
    menu.style.left = `${left}px`;
    menu.style.visibility = 'visible';

    // Close menu on click outside and stop propagation
    setTimeout(() => {
        activeMenuCloseHandler = (e) => {
            if (!menu.contains(e.target)) {
                e.stopPropagation();
                e.preventDefault();
                closeMenu();
            }
        };
        document.addEventListener('click', activeMenuCloseHandler, true);
    }, 0);
}

let activeMenuCloseHandler = null;

function closeMenu() {
    const menu = document.querySelector('.task-menu');
    if (menu) {
        menu.remove();
    }
    if (activeMenuCloseHandler) {
        document.removeEventListener('click', activeMenuCloseHandler, true);
        activeMenuCloseHandler = null;
    }
}

async function dispatchMenuAction(kind, id, action) {
    const cache = kind === 'goal' ? goalsCache : tasksCache;
    const item = cache[id];
    if (!item) {
        showToast(kind === 'goal' ? 'Goal not found' : 'Task not found', true);
        return;
    }

    closeMenu();

    if (action === 'clear_session') {
        await clearSession(kind, id);
        return;
    }

    if (kind === 'goal') {
        if (action === 'complete_goal' || action === 'defer_goal') {
            const command = action === 'complete_goal' ? 'complete-goal' : 'defer-goal';
            // complete_goal is a completed-targeting close-out — reason-free
            // (abort-only contract); the POST body carries no close-out fields.
            try {
                const body = { command };
                const response = await fetch(
                    `/api/goals/${encodeURIComponent(id)}/execute-command?vault=${encodeURIComponent(item.vault)}`,
                    {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify(body),
                    }
                );
                if (!response.ok) {
                    throw new Error(await parseErrorResponse(response));
                }
                showToast(action === 'complete_goal' ? 'Goal completed' : 'Goal deferred to tomorrow');
                await loadCurrentView();
            } catch (error) {
                console.error(`Failed to ${command}:`, error);
                showToast(error.message, true);
            }
        } else if (action === 'abort_goal') {
            const closeOut = await askCloseOut('goal', 'abort');
            if (closeOut === null) return;
            await patchStatus('goal', id, item.vault, 'aborted', 'Goal aborted', closeOut);
        } else if (action === 'hold_goal') {
            await patchStatus('goal', id, item.vault, 'hold', 'Goal on hold');
        } else if (action === 'resume_goal') {
            await patchStatus('goal', id, item.vault, 'in_progress', 'Goal resumed');
        }
        return;
    }

    // kind === 'task'
    if (action === 'complete_task' || action === 'defer_task') {
        // complete_task is a completed-targeting close-out — reason-free
        // (abort-only contract); executeSlashCommand sends no close-out fields.
        await executeSlashCommand(id, action);
    } else if (action === 'abort_task') {
        const closeOut = await askCloseOut('task', 'abort');
        if (closeOut === null) return;
        await patchStatus('task', id, item.vault, 'aborted', 'Task aborted', closeOut);
    } else if (action === 'hold_task') {
        await patchStatus('task', id, item.vault, 'hold', 'Task on hold');
    } else if (action === 'resume_task') {
        await patchStatus('task', id, item.vault, 'in_progress', 'Task resumed');
    }
}

// PATCH a task or goal status via the shared /status endpoint, toast, then refresh.
// kind: 'task' | 'goal'. Used by the card lifecycle menus for abort/hold/resume.
// closeOut ({ reason, gate_successor } | null) is set for the abort close-out
// status and added to the request body when present (completed is reason-free).
async function patchStatus(kind, id, vault, status, successMsg, closeOut = null) {
    const base = kind === 'goal' ? 'goals' : 'tasks';
    try {
        const body = { status };
        if (closeOut) {
            body.reason = closeOut.reason;
            body.gate_successor = closeOut.gate_successor;
        }
        const response = await fetch(
            `/api/${base}/${encodeURIComponent(id)}/status?vault=${encodeURIComponent(vault)}`,
            {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(body),
            }
        );
        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }
        showToast(successMsg);
        await loadCurrentView();
    } catch (error) {
        console.error(`Failed to set ${kind} status to ${status}:`, error);
        showToast(error.message, true);
    }
}

async function clearSession(kind, id) {
    // Arg-injection guard on the merged clear path (spec AC + Security).
    if (typeof id === 'string' && id.startsWith('-')) {
        showToast('Invalid id', true);
        return;
    }

    const base = kind === 'goal' ? 'goals' : 'tasks';
    const cache = kind === 'goal' ? goalsCache : tasksCache;
    const item = cache[id];
    if (!item) {
        showToast(kind === 'goal' ? 'Goal not found' : 'Task not found', true);
        return;
    }

    try {
        const response = await fetch(
            `/api/${base}/${encodeURIComponent(id)}/session?vault=${encodeURIComponent(item.vault)}`,
            { method: 'DELETE' }
        );
        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }
        if (cache[id]) {
            cache[id].claude_session_id = null;
        }
        await loadCurrentView();
    } catch (error) {
        console.error(`Failed to clear ${kind} session:`, error);
        showToast(error.message, true);
    }
}

function showToast(message, isError = false) {
    // Inject CSS on first use
    if (!document.getElementById('toast-styles')) {
        const style = document.createElement('style');
        style.id = 'toast-styles';
        style.textContent = `
            .toast {
                position: fixed;
                top: 20px;
                right: 20px;
                background: #333;
                color: #fff;
                padding: 12px 24px;
                border-radius: 6px;
                z-index: 10000;
                font-size: 14px;
                opacity: 1;
                transition: opacity 0.4s ease;
            }
            .toast.error { background: #c0392b; }
            .toast.fade-out { opacity: 0; }
        `;
        document.head.appendChild(style);
    }

    const toast = document.createElement('div');
    toast.className = 'toast' + (isError ? ' error' : '');
    toast.textContent = message;
    document.body.appendChild(toast);

    const duration = isError ? 4000 : 2000;
    setTimeout(() => {
        toast.classList.add('fade-out');
        setTimeout(() => toast.remove(), 400);
    }, duration);
}

async function executeSlashCommand(taskId, commandType) {
    const task = tasksCache[taskId];
    if (!task) {
        showToast('Task not found', true);
        return;
    }

    // Show loading modal
    const loadingModal = document.getElementById('loading-modal');
    loadingModal.classList.remove('hidden');

    // Track if user dismissed loading modal
    let userDismissed = false;

    // Setup close button handler
    const closeBtn = document.getElementById('close-loading-btn');
    const closeHandler = () => {
        userDismissed = true;
        loadingModal.classList.add('hidden');
        closeBtn.removeEventListener('click', closeHandler);
    };
    closeBtn.addEventListener('click', closeHandler);

    try {
        // Map action to slash command
        const commandMap = {
            'complete_task': 'complete-task',
            'defer_task': 'defer-task'
        };
        const slashCommand = commandMap[commandType];

        // Call backend endpoint. Neither complete-task nor defer-task carries
        // close-out fields (completion is reason-free; defer never had any).
        const body = { command: slashCommand };
        const response = await fetch(
            `/api/tasks/${encodeURIComponent(taskId)}/execute-command?vault=${encodeURIComponent(task.vault)}`,
            {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(body),
            }
        );

        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }

        const data = await response.json();

        // Cleanup
        closeBtn.removeEventListener('click', closeHandler);

        // Hide loading modal
        loadingModal.classList.add('hidden');

        // vault-cli fast path: empty session_id means instant execution
        if (!data.session_id) {
            if (!data.success || data.error) {
                showToast(data.error || 'Command failed', true);
            } else {
                const successMessage = commandType === 'defer_task' ? 'Task deferred' : 'Task completed';
                showToast(successMessage);
                loadCurrentView();
            }
        } else if (!userDismissed) {
            // Only show session modal if user didn't dismiss loading modal
            showModal(data.session_id, data.command, data.working_dir, data.task_title, data.executed_command, data.success, data.error);
        }

    } catch (error) {
        // Cleanup
        closeBtn.removeEventListener('click', closeHandler);

        // Hide loading modal
        loadingModal.classList.add('hidden');
        await new Promise(r => requestAnimationFrame(r));  // ensure modal hides before toast renders

        console.error('Error executing slash command:', error);
        showToast(error.message, true);
    }
}

// WebSocket functions for real-time updates

// False until the socket opens for the first time. Distinguishes the initial
// connect (whose data the page load already fetched) from a reconnect (which
// must re-fetch, because events during the gap are lost, not queued).
let wsHasConnected = false;

function connectWebSocket() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/ws`;

    ws = new WebSocket(wsUrl);

    ws.onopen = () => {
        console.log('WebSocket connected');
        updateConnectionStatus(true);
        // Catch-up re-fetch on RECONNECT only. Events emitted while the socket was
        // down are never replayed, so without this the tab renders its stale copy
        // until the user manually refreshes — the card sits on "Starting…" even
        // though claude_session_id already landed. Skipped on the first connect,
        // where the initial load has just fetched the same data.
        if (wsHasConnected) {
            console.log('WebSocket reconnected — re-fetching to catch up on missed events');
            loadCurrentView();
        }
        wsHasConnected = true;
    };

    ws.onmessage = (event) => {
        const data = JSON.parse(event.data);
        console.log('WebSocket message received:', data);
        handleTaskUpdate(data);
    };

    ws.onerror = (error) => {
        console.error('WebSocket error:', error);
        updateConnectionStatus(false);
    };

    ws.onclose = () => {
        console.log('WebSocket disconnected, reconnecting in 3s...');
        updateConnectionStatus(false);
        setTimeout(connectWebSocket, 3000);  // Auto-reconnect
    };
}

function handleTaskUpdate(data) {
    const { type, vault, item_kind } = data;
    // Goal events use `goal_id`, task events use `task_id`. Accept either
    // so the frontend works during the deploy window where some payloads
    // still carry `task_id` for goal events.
    const id = data.goal_id || data.task_id;
    // Pre-prompt-3 payloads have no item_kind; default to "task" so
    // pre-existing event types (task_updated etc.) keep working during
    // the deploy window. With prompt 3 shipped, every payload from the
    // running orchestrator carries item_kind; warn once if we see a
    // payload that omits it (likely a pre-prompt-3 backend in flight).
    let kind = item_kind;
    if (!kind) {
        console.warn('WebSocket payload missing item_kind; defaulting to "task" (pre-prompt-3 backend?)');
        kind = 'task';
    }

    // Check if update is for a vault we're displaying
    const shouldUpdate = currentVault === null ||
                         currentVault === vault ||
                         (Array.isArray(currentVault) && currentVault.includes(vault));
    if (!shouldUpdate) {
        console.log(`Ignoring ${kind} update for vault ${vault} (current: ${JSON.stringify(currentVault)})`);
        return;
    }

    console.log(`Handling ${type} event for ${kind} ${id}`);

    // Dispatch by kind — only re-fetch the active view's data.
    // This is the spec AC#9 invariant: editing a task does NOT trigger
    // a goals re-fetch, and vice versa.
    if (kind === 'goal') {
        if (currentView === 'goals') {
            if (type === 'deleted') {
                removeGoalCard(id);
            } else {
                loadGoals();
            }
        }
        // else: user is on Tasks view, ignore the goal event
    } else {
        // kind === 'task' (or anything else — backwards compat)
        if (currentView === 'goals') {
            // Spec 014 AC#3: a task event arriving while on Goals view does
            // NOT mutate the goals DOM and does NOT trigger any fetch. Return
            // explicitly so future edits cannot accidentally re-fetch goals
            // in response to a task event (and vice versa).
            console.log(`Ignoring task event for ${id} — view is goals`);
            return;
        }
        if (currentView === 'tasks') {
            if (type === 'deleted') {
                removeTaskCard(id);
            } else {
                loadCurrentView();
            }
        }
    }
}

function removeTaskCard(taskId) {
    // Find and remove the task card from DOM
    const card = document.querySelector(`[data-task-id="${taskId}"]`);
    if (card) {
        card.remove();
        console.log(`Removed task card: ${taskId}`);
    }

    // Remove from cache
    if (tasksCache[taskId]) {
        delete tasksCache[taskId];
    }
}

function removeGoalCard(goalId) {
    const card = document.querySelector(`[data-goal-id="${goalId}"]`);
    if (card) card.remove();
    if (goalsCache[goalId]) delete goalsCache[goalId];
}

function setView(newView) {
    if (newView !== 'tasks' && newView !== 'goals') return;
    currentView = newView;
    // Grouping follows the view: tasks→phase, goals→status. No user override.
    currentGroupBy = newView === 'goals' ? 'status' : 'phase';
    // Reset the status filter to the kind-aware default when toggling views so
    // that switching from Tasks (in_progress + completed) to Goals doesn't leave
    // BACKLOG / NEXT columns empty (and vice versa). The operator can still
    // narrow afterwards via the status dropdown.
    currentStatuses = newView === 'goals'
        ? ['backlog', 'next', 'in_progress', 'hold', 'completed']
        : ['in_progress', 'hold', 'completed'];
    updateStatusLabel();
    renderStatusDropdown();
    updateViewToggle();
    renderColumnHeaders();
    updateURL();
    loadCurrentView();
}

function renderColumnHeaders() {
    const board = document.querySelector('.kanban-board');
    if (!board) return;

    if (currentGroupBy === 'status') {
        // Show status columns, hide phase columns.
        board.classList.add('status-mode');
        // Remove any pre-existing status columns (idempotent).
        board.querySelectorAll('[data-status]').forEach(el => el.remove());
        // Insert status columns at the start of the board (in canonical enum order).
        // Visible status columns (left → right, time-progression).
        // Hold + aborted are hidden by DEFAULT (rare edge states), but must get a
        // column when the operator explicitly filters them in — otherwise a held /
        // aborted goal is fetched but has no column to render into and silently
        // vanishes (its card, and thus its Resume action, becomes unreachable).
        const STATUS_COLUMNS = [
            { id: 'backlog', label: 'Backlog' },
            { id: 'next', label: 'Next' },
            { id: 'in_progress', label: 'In Progress' },
            { id: 'completed', label: 'Completed' },
        ];
        if (currentStatuses.includes('hold')) {
            STATUS_COLUMNS.push({ id: 'hold', label: 'Hold' });
        }
        if (currentStatuses.includes('aborted')) {
            STATUS_COLUMNS.push({ id: 'aborted', label: 'Aborted' });
        }
        STATUS_COLUMNS.forEach(col => {
            const div = document.createElement('div');
            div.className = 'kanban-column';
            div.dataset.status = col.id;
            div.innerHTML = `<h2 data-column-header="${col.id}">${col.label}</h2><div class="cards" id="cards-${col.id}"></div>`;
            board.appendChild(div);
        });
        // Add the "—" (unknown) column ONLY for goals view under status mode
        // — no, actually under status mode EVERY goal has a status, so no
        // "—" column. Reserved for the phase-on-goals fallback below.
    } else {
        // phase mode: hide status columns, show phase columns
        board.classList.remove('status-mode');
        board.querySelectorAll('[data-status]').forEach(el => el.remove());
        // Restore phase column headers from data-phase attribute (in case
        // they were mutated). The header text comes from a fixed map.
        const PHASE_HEADERS = {
            'todo': 'Todo',
            'planning': 'Planning',
            'in_progress': 'Execution',
            'execution': 'Execution',
            'ai_review': 'AI Review',
            'human_review': 'Human Review',
            'done': 'Done',
        };
        board.querySelectorAll('.kanban-column[data-phase]').forEach(col => {
            const phase = col.dataset.phase;
            const h2 = col.querySelector('h2');
            if (h2 && PHASE_HEADERS[phase]) {
                h2.textContent = PHASE_HEADERS[phase];
                h2.dataset.columnHeader = phase;
            }
        });

        // For goals view under phase mode, add the "—" column (only if
        // any goal might lack a phase). The column exists permanently
        // under ?view=goals&groupBy=phase; the column is removed under
        // other combinations.
        const unknownCol = board.querySelector('.kanban-column[data-phase="unknown"]');
        if (currentView === 'goals') {
            if (!unknownCol) {
                const div = document.createElement('div');
                div.className = 'kanban-column';
                div.dataset.phase = 'unknown';
                div.innerHTML = '<h2 data-column-header="unknown">—</h2><div class="cards" id="cards-unknown"></div>';
                board.appendChild(div);
            }
        } else {
            // Tasks view: never show the "—" column
            if (unknownCol) unknownCol.remove();
        }
    }
    // Status columns are destroyed and recreated on every call (status mode);
    // re-wire drop handlers so goal drag-and-drop survives a view switch.
    // Idempotent for the surviving static phase columns (addEventListener
    // de-dups identical named-handler triples).
    setupDragAndDrop();
}

function updateViewToggle() {
    const buttons = document.querySelectorAll('.view-toggle-btn');
    buttons.forEach(btn => {
        const isActive = btn.dataset.view === currentView;
        btn.classList.toggle('active', isActive);
        btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
    });
}

function updateConnectionStatus(connected) {
    const statusEl = document.getElementById('ws-status');
    if (statusEl) {
        if (connected) {
            statusEl.classList.remove('disconnected');
            statusEl.title = 'WebSocket connected';
        } else {
            statusEl.classList.add('disconnected');
            statusEl.title = 'WebSocket disconnected';
        }
    }
}
