// TaskOrchestrator Kanban Board

let currentVault = null; // null = "All", or vault name
let currentAssignees = [];
let currentStatuses = ['in_progress', 'completed']; // default — overridden by ?status= URL param
let currentGoals = []; // goal filter from URL — empty means no filter
// Distinct assignees across the selected vaults — sourced from /api/assignees,
// refreshed on startup and on every vault-selector change. Read by computeAssigneeOptions.
let availableAssignees = { named: [], hasUnassigned: false };
const ALL_STATUSES = ['next', 'in_progress', 'backlog', 'completed', 'hold', 'aborted']; // closed enum, fixed display order
let tasksCache = {}; // Map of task ID -> task data
let ws = null; // WebSocket connection
let startingTasks = new Set(); // Track tasks currently being started

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
    loadVaults();
    setupEventListeners();
    connectWebSocket();
    startPolling();
});

// Fallback polling in case WebSocket misses updates
function startPolling() {
    setInterval(() => {
        console.log('Polling for task updates...');
        loadTasks();
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

    // Parse status parameter(s) — supports repeated form and comma-separated form
    // (backend handles comma-split server-side); absent param keeps the default.
    const statusParams = params.getAll('status');
    if (statusParams.length > 0) {
        currentStatuses = statusParams;
    }

    // Parse goal parameter(s) — supports repeated form (?goal=A&goal=B)
    currentGoals = params.getAll('goal');
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
    document.getElementById('refresh-btn').addEventListener('click', loadTasks);
    document.getElementById('copy-btn').addEventListener('click', copyCommand);
    document.getElementById('close-btn').addEventListener('click', closeModal);
    setupModalBackdropClose();
    setupDragAndDrop();
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
    loadTasks();
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
    loadTasks();
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
    loadTasks();
    // loadTasks will re-render the dropdown; no need to do it here.
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
    loadTasks();
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
                loadTasks();
            });
            dropdown.appendChild(item);
        });

        updateVaultLabel();
        updateStatusLabel();
        updateAssigneeLabel();

        // Load assignee options before tasks so the dropdown renders against the full set on first paint.
        await loadAssignees();

        // Load tasks
        await loadTasks();
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
    loadTasks();
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
    loadTasks();
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

    // Reload tasks
    loadTasks();
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
        await loadTasks();
    } catch (err) {
        console.error('Assign to me network error:', err);
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

    const taskId = e.dataTransfer.getData('text/plain');
    const task = tasksCache[taskId];

    if (!task) {
        showToast('Task not found', true);
        return;
    }

    const newPhase = e.currentTarget.id.replace('cards-', '');

    try {
        const response = await fetch(`/api/tasks/${taskId}/phase?vault=${encodeURIComponent(task.vault)}`, {
            method: 'PATCH',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({ phase: newPhase }),
        });

        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }

        // Reload tasks to reflect changes
        await loadTasks();
    } catch (error) {
        console.error('Failed to update task phase:', error);
        showToast(error.message, true);
    }
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

        // Clear existing cards
        ['todo', 'planning', 'execution', 'ai_review', 'human_review', 'done'].forEach(phase => {
            const container = document.getElementById(`cards-${phase}`);
            if (container) {
                container.innerHTML = '';
            }
        });

        // Sort tasks by urgency tier first (0=overdue, 1=due-today, 2=scheduled, 3=none),
        // then by priority within each tier (high=1, medium=2, low=3, null=999)
        tasks.sort((a, b) => {
            const urgencyA = getUrgencyTier(a);
            const urgencyB = getUrgencyTier(b);
            if (urgencyA !== urgencyB) return urgencyA - urgencyB;
            return normalizePriority(a.priority) - normalizePriority(b.priority);
        });

        // Split tasks: active, upcoming (deferred soon), recently_completed (done lane only)
        const activeTasks = tasks.filter(t => !t.upcoming && !t.recently_completed);
        const upcomingTasks = tasks.filter(t => t.upcoming);
        const recentlyCompletedTasks = tasks.filter(t => t.recently_completed);

        // Populate cards: active first, then upcoming per lane, recently-completed always at bottom of done
        const validPhases = ['todo', 'planning', 'execution', 'ai_review', 'human_review', 'done'];
        [...activeTasks, ...upcomingTasks].forEach(task => {
            // One-way display alias: on-disk in_progress renders in the execution column.
            const displayPhase = task.phase === 'in_progress' ? 'execution' : task.phase;
            // Default to todo if phase is missing or invalid
            const phase = displayPhase && validPhases.includes(displayPhase) ? displayPhase : 'todo';
            const container = document.getElementById(`cards-${phase}`);
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

        // Refresh the assignee dropdown so options reflect the freshly loaded data.
        renderAssigneeDropdown();
        updateAssigneeLabel();

    } catch (error) {
        console.error('Failed to load tasks:', error);
        showToast(error.message, true);
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

    // Drag handlers
    card.addEventListener('dragstart', (e) => {
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', task.id);
        card.classList.add('dragging');
    });

    card.addEventListener('dragend', (e) => {
        card.classList.remove('dragging');
    });

    // Extract Jira issue info
    const { title, issueKey, issueUrl } = extractJiraIssue(task.title);

    // Show Resume button if session exists, Starting if in progress, otherwise Start.
    // Treat startingTasks as a hint, not truth: if claude_session_id is already set
    // (watcher event arrived, backend completed) the Set entry is stale.
    const hasSession = task.claude_session_id;
    const isStarting = startingTasks.has(task.id) && !hasSession;
    let buttonLabel, buttonClass, buttonDisabled;
    if (isStarting) {
        buttonLabel = '⏳ Starting...';
        buttonClass = 'start-btn';
        buttonDisabled = true;
    } else if (hasSession) {
        buttonLabel = '▶ Resume';
        buttonClass = 'resume-btn';
        buttonDisabled = false;
    } else {
        buttonLabel = '▶ Start';
        buttonClass = 'start-btn';
        buttonDisabled = false;
    }
    const startButton = `<button class="${buttonClass}" onclick="runTask('${task.id}')" ${buttonDisabled ? 'disabled' : ''}>${buttonLabel}</button>`;

    const menuButton = '<button class="menu-btn" onclick="showTaskMenu(event, \'' + task.id + '\')">⋮</button>';

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

    card.innerHTML = `
        ${menuButton}
        <div class="card-content">
            <h3 class="task-title">
                <a href="${task.obsidian_url}" class="task-title-link" title="Open in Obsidian">
                    ${escapeHtml(title)}
                    <span class="obsidian-icon">↗</span>
                </a>
            </h3>
        </div>
        <div class="card-footer">
            <div class="card-footer-left">
                ${jiraBadge}
                ${assigneeBadge}
            </div>
            <div class="card-actions">
                ${startButton}
            </div>
        </div>
    `;

    return card;
}

async function runTask(taskId) {
    // Look up task from cache
    const task = tasksCache[taskId];
    if (!task) {
        showToast('Task not found in cache', true);
        return;
    }

    try {
        // Show loading state
        const button = event.target;
        const originalText = button.textContent;
        button.textContent = '⏳ Loading...';
        button.disabled = true;

        // If task already has a session, show resume modal directly
        if (task.claude_session_id) {
            // Get vault config to build command
            const vaultsResponse = await fetch('/api/vaults');
            const vaults = await vaultsResponse.json();
            const vaultConfig = vaults.find(v => v.name === task.vault);

            if (!vaultConfig) {
                throw new Error('Vault not found');
            }

            const command = `${vaultConfig.claude_script} --resume ${task.claude_session_id}`;
            showModal(task.claude_session_id, command, vaultConfig.vault_path, task.title);

            // Restore button
            button.textContent = originalText;
            button.disabled = false;
            return;
        }

        // Show loading modal during session creation
        const loadingModal = document.getElementById('loading-modal');
        loadingModal.classList.remove('hidden');

        // Setup close button handler
        let userDismissed = false;
        const closeBtn = document.getElementById('close-loading-btn');
        const closeHandler = () => {
            userDismissed = true;
            loadingModal.classList.add('hidden');
            closeBtn.removeEventListener('click', closeHandler);
            // Clear the in-flight marker so the next render reflects the actual
            // backend state (Resume once claude_session_id lands, Start otherwise).
            startingTasks.delete(taskId);
            renderTasks();
        };
        closeBtn.addEventListener('click', closeHandler);

        // Create new Claude session
        startingTasks.add(taskId);
        button.textContent = '⏳ Starting...';
        const response = await fetch(`/api/tasks/${taskId}/run?vault=${encodeURIComponent(task.vault)}`, {
            method: 'POST'
        });

        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }

        const data = await response.json();

        // Cleanup
        closeBtn.removeEventListener('click', closeHandler);
        loadingModal.classList.add('hidden');

        // Done starting
        startingTasks.delete(taskId);

        // Update task cache with new session_id
        task.claude_session_id = data.session_id;

        // Show session modal with command (unless user dismissed loading)
        if (!userDismissed) {
            showModal(data.session_id, data.command, data.working_dir, data.task_title);
        }

        // Restore button and update to Resume
        button.textContent = '▶ Resume';
        button.className = 'resume-btn';
        button.disabled = false;

    } catch (error) {
        console.error('Failed to run task:', error);

        // Clear starting state and hide loading modal on error
        startingTasks.delete(taskId);
        const loadingModal = document.getElementById('loading-modal');
        loadingModal.classList.add('hidden');
        await new Promise(r => requestAnimationFrame(r));  // ensure modal hides before toast renders

        showToast(error.message, true);

        // Restore button
        if (event && event.target) {
            event.target.textContent = '▶ Start';
            event.target.disabled = false;
        }
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

function showTaskMenu(event, taskId) {
    event.stopPropagation();

    // Remove any existing menu
    const existingMenu = document.querySelector('.task-menu');
    if (existingMenu) {
        existingMenu.remove();
    }

    // Create menu
    const menu = document.createElement('div');
    menu.className = 'task-menu';

    // Get task from cache to check if it has a session
    const task = tasksCache[taskId];
    const hasSession = task && task.claude_session_id;

    const menuItems = [];

    // Add Clear Session option if task has a session
    if (hasSession) {
        menuItems.push({ label: 'Clear Session', action: 'clear_session', disabled: false });
    }

    // Add slash command actions
    menuItems.push({ label: 'Complete Task', action: 'complete_task', disabled: false });
    menuItems.push({ label: 'Defer Task', action: 'defer_task', disabled: false });

    // Add phase options
    menuItems.push({ label: 'Move to', action: 'move', disabled: false });
    menuItems.push({ label: 'Error', action: 'error', disabled: true });
    menuItems.push({ label: 'Execution', action: 'execution', disabled: false });
    menuItems.push({ label: 'AI Review', action: 'ai_review', disabled: false });
    menuItems.push({ label: 'Human Review', action: 'human_review', disabled: false });
    menuItems.push({ label: 'Done', action: 'done', disabled: false });

    menuItems.forEach(item => {
        const menuItem = document.createElement('div');
        menuItem.className = 'task-menu-item';
        if (item.disabled) {
            menuItem.classList.add('disabled');
        }
        if (item.label === 'Move to') {
            menuItem.classList.add('header');
        }
        menuItem.textContent = item.label;

        if (!item.disabled && item.action !== 'move') {
            menuItem.addEventListener('click', () => handleMenuAction(taskId, item.action));
        }

        menu.appendChild(menuItem);
    });

    // Position menu
    const button = event.target;
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

async function handleMenuAction(taskId, action) {
    const task = tasksCache[taskId];
    if (!task) {
        showToast('Task not found', true);
        return;
    }

    closeMenu();

    if (action === 'clear_session') {
        await clearTaskSession(taskId);
    } else if (action === 'complete_task' || action === 'defer_task') {
        // Handle slash commands
        await executeSlashCommand(taskId, action);
    } else {
        // Move to phase
        try {
            const response = await fetch(`/api/tasks/${taskId}/phase?vault=${encodeURIComponent(task.vault)}`, {
                method: 'PATCH',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({ phase: action }),
            });

            if (!response.ok) {
                throw new Error(await parseErrorResponse(response));
            }

            await loadTasks();
        } catch (error) {
            console.error('Failed to update task phase:', error);
            showToast(error.message, true);
        }
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

        // Call backend endpoint
        const response = await fetch(
            `/api/tasks/${encodeURIComponent(taskId)}/execute-command?vault=${encodeURIComponent(task.vault)}`,
            {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ command: slashCommand }),
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
                loadTasks();
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

async function clearTaskSession(taskId) {
    const task = tasksCache[taskId];
    if (!task) {
        showToast('Task not found', true);
        return;
    }

    try {
        const response = await fetch(`/api/tasks/${taskId}/session?vault=${encodeURIComponent(task.vault)}`, {
            method: 'DELETE',
        });

        if (!response.ok) {
            throw new Error(await parseErrorResponse(response));
        }

        // Update cache
        if (tasksCache[taskId]) {
            tasksCache[taskId].claude_session_id = null;
        }

        // Reload tasks to update UI
        await loadTasks();
    } catch (error) {
        console.error('Failed to clear session:', error);
        showToast(error.message, true);
    }
}

// WebSocket functions for real-time updates
function connectWebSocket() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/ws`;

    ws = new WebSocket(wsUrl);

    ws.onopen = () => {
        console.log('WebSocket connected');
        updateConnectionStatus(true);
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
    const { type, task_id, vault } = data;

    // Check if update is for a vault we're displaying
    const shouldUpdate = currentVault === null || // All vaults
                         currentVault === vault || // Single vault match
                         (Array.isArray(currentVault) && currentVault.includes(vault)); // Multiple vaults

    if (!shouldUpdate) {
        console.log(`Ignoring update for vault ${vault} (current: ${JSON.stringify(currentVault)})`);
        return;
    }

    console.log(`Handling ${type} event for task ${task_id}`);

    switch (type) {
        case 'modified':
        case 'created':
            // Reload all tasks to get updated data
            loadTasks();
            break;
        case 'deleted':
            // Remove task card from UI
            removeTaskCard(task_id);
            break;
        case 'moved':
            // Reload tasks (task renamed)
            loadTasks();
            break;
        default:
            console.warn(`Unknown event type: ${type}`);
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
