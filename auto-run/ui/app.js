/* Auto-Run Benchmark Harness — UI */
'use strict';

const API = '';  // same origin via port-forward

// ── State ────────────────────────────────────────────────
let state = {
  runs: [],       // []Run from /api/matrix
  defaults: {},
  selectedID: null,
  sseSource: null,
  sseRunID: null,
  orchRunning: false,
  sessionID: '',
  editingID: null,  // null = adding, string = editing
};

// ── Bootstrap ────────────────────────────────────────────
(async () => {
  await Promise.all([loadMatrix(), loadStatus()]);
  renderRunList();
  loadSettings();
  pollStatus();
})();

// ── Data loading ─────────────────────────────────────────
async function loadMatrix() {
  const res = await fetch(`${API}/api/matrix`);
  if (!res.ok) return;
  const data = await res.json();
  state.runs = data.runs || [];
  state.defaults = data.defaults || {};
}

async function loadStatus() {
  const res = await fetch(`${API}/api/status`);
  if (!res.ok) return;
  const data = await res.json();
  state.orchRunning = data.running;
  state.sessionID = data.session_id || '';
  updateOrchestratorUI();
}

async function loadSettings() {
  const res = await fetch(`${API}/api/settings`);
  if (!res.ok) return;
  const d = await res.json();
  const form = document.getElementById('settings-form');
  for (const [k, v] of Object.entries(d)) {
    const el = form.querySelector(`[name="${k}"]`);
    if (el) el.value = v;
  }
}

function pollStatus() {
  setInterval(async () => {
    await loadStatus();
    // Refresh run list to pick up status changes from SSE fallback.
    const res = await fetch(`${API}/api/matrix`);
    if (!res.ok) return;
    const data = await res.json();
    state.runs = data.runs || [];
    renderRunList();
    if (state.selectedID) renderDetail(state.selectedID);
  }, 3000);
}

// ── Run list ──────────────────────────────────────────────
function renderRunList() {
  const list = document.getElementById('run-list');
  list.innerHTML = '';
  if (!state.runs.length) {
    list.innerHTML = '<p style="color:var(--text2);padding:8px;font-size:12px">No runs. Click ＋ Add Run.</p>';
    return;
  }
  state.runs.forEach(run => {
    const card = document.createElement('div');
    card.className = 'run-card' + (run.id === state.selectedID ? ' active' : '');
    card.dataset.id = run.id;
    card.draggable = true;
    card.innerHTML = `
      <div class="run-card-header">
        <span class="run-id" title="${run.id}">${run.id}</span>
        <span class="status-badge badge-${run.status}">${run.status}</span>
      </div>
      <div class="run-card-meta">${run.config} × ${run.scenario}</div>
      ${(run.tags||[]).length ? `<div class="run-card-tags">${run.tags.map(t=>`<span class="tag">${t}</span>`).join('')}</div>` : ''}
    `;
    card.addEventListener('click', () => selectRun(run.id));
    card.addEventListener('dragstart', onDragStart);
    card.addEventListener('dragover',  onDragOver);
    card.addEventListener('dragleave', onDragLeave);
    card.addEventListener('drop',      onDrop);
    card.addEventListener('dragend',   onDragEnd);
    list.appendChild(card);
  });
}

// ── Run detail ────────────────────────────────────────────
function selectRun(id) {
  state.selectedID = id;
  renderRunList();
  renderDetail(id);
  subscribeSSE(id);
}

function renderDetail(id) {
  const run = state.runs.find(r => r.id === id);
  if (!run) return;

  document.getElementById('no-run-msg').style.display = 'none';
  const detail = document.getElementById('run-detail');
  detail.style.display = 'flex';

  // Steps
  const bar = document.getElementById('steps-bar');
  bar.innerHTML = (run.steps || []).map(s => {
    const icon = stepIcon(s.status);
    return `<span class="step-chip ${s.status}"><span class="step-icon">${icon}</span>${s.name}</span>`;
  }).join('');

  // Config / scenario tab (lazy load file contents via path display)
  document.getElementById('config-yaml').textContent = run.config;
  document.getElementById('scenario-yaml').textContent = run.scenario;
}

function stepIcon(status) {
  return { pending: '○', running: '◎', done: '✓', error: '✗' }[status] || '○';
}

// ── SSE log streaming ─────────────────────────────────────
function subscribeSSE(id) {
  if (state.sseSource) {
    state.sseSource.close();
    state.sseSource = null;
  }
  if (state.sseRunID !== id) {
    document.getElementById('log-box').textContent = '';
  }
  state.sseRunID = id;

  const es = new EventSource(`${API}/api/runs/${encodeURIComponent(id)}/logs`);
  state.sseSource = es;

  es.onmessage = (e) => {
    try {
      const ev = JSON.parse(e.data);
      if (ev.type === 'log') appendLog(ev.payload);
      else if (ev.type === 'status') {
        const run = state.runs.find(r => r.id === ev.run_id);
        if (run) { run.status = ev.payload; renderRunList(); }
      } else if (ev.type === 'step') {
        const [name, status] = ev.payload.split(':');
        const run = state.runs.find(r => r.id === ev.run_id);
        if (run && run.steps) {
          const step = run.steps.find(s => s.name === name);
          if (step) step.status = status;
          renderDetail(ev.run_id);
        }
      }
    } catch { /* ignore parse errors */ }
  };
  es.onerror = () => {
    // SSE disconnected; polling will keep the state fresh.
  };
}

function appendLog(line) {
  const box = document.getElementById('log-box');
  const ts = new Date().toISOString().slice(11, 19);
  const entry = document.createElement('span');
  entry.innerHTML = `<span class="ts">${ts} </span>${escapeHtml(line)}\n`;
  box.appendChild(entry);
  const scroll = document.getElementById('autoscroll');
  if (scroll && scroll.checked) box.scrollTop = box.scrollHeight;
}

// ── Orchestrator controls ─────────────────────────────────
async function sendControl(action) {
  await fetch(`${API}/api/control`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ action }),
  });
  await loadStatus();
}

document.getElementById('btn-start').addEventListener('click', () => sendControl('start'));
document.getElementById('btn-pause').addEventListener('click', () => sendControl('pause'));
document.getElementById('btn-stop').addEventListener('click',  () => sendControl('stop'));

document.getElementById('btn-retry').addEventListener('click', async () => {
  if (!state.selectedID) return;
  // Reset this single run by sending retry action (orchestrator resets all failed).
  await sendControl('retry');
  await loadMatrix();
  renderRunList();
});

document.getElementById('btn-delete-run').addEventListener('click', async () => {
  if (!state.selectedID) return;
  if (!confirm(`Delete run "${state.selectedID}"?`)) return;
  await fetch(`${API}/api/runs/${encodeURIComponent(state.selectedID)}`, { method: 'DELETE' });
  state.selectedID = null;
  document.getElementById('run-detail').style.display = 'none';
  document.getElementById('no-run-msg').style.display = '';
  await loadMatrix();
  renderRunList();
});

function updateOrchestratorUI() {
  const badge = document.getElementById('orch-status');
  badge.className = 'status-badge ' + (state.orchRunning ? 'badge-running' : 'badge-queued');
  badge.textContent = state.orchRunning ? 'running' : 'idle';
  document.getElementById('btn-start').disabled = state.orchRunning;
  document.getElementById('btn-pause').disabled = !state.orchRunning;
  document.getElementById('btn-stop').disabled  = !state.orchRunning;
  document.getElementById('session-label').textContent =
    state.sessionID ? `session: ${state.sessionID}` : 'session: —';
}

// ── Tab switching ─────────────────────────────────────────
document.querySelectorAll('.tab-btn').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
    document.querySelectorAll('.tab-panel').forEach(p => p.classList.remove('active'));
    btn.classList.add('active');
    document.getElementById('tab-' + btn.dataset.tab).classList.add('active');
  });
});

// ── Settings ──────────────────────────────────────────────
document.getElementById('settings-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const form = e.target;
  const data = {
    concurrency: parseInt(form.concurrency.value, 10) || 100,
    db_url: form.db_url.value,
    prometheus_url: form.prometheus_url.value,
    gcs_bucket: form.gcs_bucket.value,
    worker_node: form.worker_node.value,
    scale_factor: parseInt(form.scale_factor.value, 10) || 0,
  };
  const res = await fetch(`${API}/api/settings`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  const err = document.getElementById('settings-error');
  err.textContent = res.ok ? '✓ Saved' : 'Failed to save';
  if (res.ok) setTimeout(() => err.textContent = '', 2000);
});

// ── Add/Edit Run Modal ────────────────────────────────────
document.getElementById('btn-add-run').addEventListener('click', () => openModal(null));

function openModal(id) {
  state.editingID = id;
  document.getElementById('modal-title').textContent = id ? 'Edit Run' : 'Add Run';
  document.getElementById('modal-error').textContent = '';
  const run = id ? state.runs.find(r => r.id === id) : null;
  document.getElementById('modal-id').value = run?.id || '';
  document.getElementById('modal-id').disabled = !!id;
  document.getElementById('modal-config').value = run?.config || '';
  document.getElementById('modal-scenario').value = run?.scenario || '';
  document.getElementById('modal-tags').value = (run?.tags || []).join(', ');
  document.getElementById('modal-concurrency').value = run?.concurrency || 0;
  document.getElementById('modal-scale-factor').value = run?.scale_factor || 0;
  document.getElementById('modal-worker-node').value = run?.worker_node || '';
  document.getElementById('run-modal').classList.add('open');
}

document.getElementById('modal-cancel').addEventListener('click', () => {
  document.getElementById('run-modal').classList.remove('open');
});

document.getElementById('modal-save').addEventListener('click', async () => {
  const id = document.getElementById('modal-id').value.trim();
  const config = document.getElementById('modal-config').value.trim();
  const scenario = document.getElementById('modal-scenario').value.trim();
  const tagsRaw = document.getElementById('modal-tags').value.trim();
  const concurrency = parseInt(document.getElementById('modal-concurrency').value, 10) || 0;
  const scaleFactor = parseInt(document.getElementById('modal-scale-factor').value, 10) || 0;
  const workerNode = document.getElementById('modal-worker-node').value.trim();
  const errEl = document.getElementById('modal-error');

  if (!id || !config || !scenario) {
    errEl.textContent = 'ID, config, and scenario are required.';
    return;
  }

  const spec = {
    id, config, scenario,
    tags: tagsRaw ? tagsRaw.split(',').map(t => t.trim()).filter(Boolean) : [],
    concurrency: concurrency || undefined,
    scale_factor: scaleFactor || undefined,
    worker_node: workerNode || undefined,
  };

  let res;
  if (state.editingID) {
    res = await fetch(`${API}/api/runs/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(spec),
    });
  } else {
    // Add by replacing full matrix
    const mf = {
      defaults: state.defaults,
      runs: [...state.runs.map(r => ({
        id: r.id, config: r.config, scenario: r.scenario,
        tags: r.tags, concurrency: r.concurrency,
        scale_factor: r.scale_factor, worker_node: r.worker_node,
      })), spec],
    };
    res = await fetch(`${API}/api/matrix`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(mf),
    });
  }

  if (!res.ok) {
    const text = await res.text();
    errEl.textContent = text;
    return;
  }
  document.getElementById('run-modal').classList.remove('open');
  await loadMatrix();
  renderRunList();
});

// ── Drag-and-drop reorder ─────────────────────────────────
let dragSrcID = null;

function onDragStart(e) {
  dragSrcID = e.currentTarget.dataset.id;
  e.currentTarget.classList.add('dragging');
  e.dataTransfer.effectAllowed = 'move';
}
function onDragOver(e) {
  e.preventDefault();
  e.dataTransfer.dropEffect = 'move';
  e.currentTarget.classList.add('drag-over');
}
function onDragLeave(e) { e.currentTarget.classList.remove('drag-over'); }
function onDragEnd(e)  { e.currentTarget.classList.remove('dragging'); }

async function onDrop(e) {
  e.preventDefault();
  const targetID = e.currentTarget.dataset.id;
  e.currentTarget.classList.remove('drag-over');
  if (!dragSrcID || dragSrcID === targetID) return;

  // Move dragSrcID after targetID.
  await fetch(`${API}/api/runs/${encodeURIComponent(dragSrcID)}/move`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ after: targetID }),
  });
  await loadMatrix();
  renderRunList();
  dragSrcID = null;
}

// ── File Manager ─────────────────────────────────────────
let currentFileCategory = 'configs';

document.getElementById('file-category').addEventListener('change', (e) => {
  currentFileCategory = e.target.value;
  loadFiles();
});

document.querySelector('.tab-btn[data-tab="files"]').addEventListener('click', loadFiles);

async function loadFiles() {
  const cat = currentFileCategory;
  const res = await fetch(`${API}/api/files/${cat}`);
  if (!res.ok) return;
  const files = await res.json();
  renderFileList(files || []);
}

function renderFileList(files) {
  const list = document.getElementById('file-list');
  if (!files.length) {
    list.innerHTML = '<p style="color:var(--text2);font-size:12px">No files uploaded yet.</p>';
    return;
  }
  list.innerHTML = files.map(f => `
    <div class="file-row">
      <span class="file-name" data-name="${f.name}" data-cat="${f.category}"
            onclick="previewFile('${f.category}','${f.name}')">${f.name}</span>
      <span class="file-size">${formatBytes(f.size)}</span>
      <span class="file-date">${formatDate(f.updated_at)}</span>
      <button class="file-use-btn" onclick="useFileInRun('${f.category}','${f.name}')">Use in run</button>
      <button class="file-del-btn" title="Delete" onclick="deleteFile('${f.category}','${f.name}')">✕</button>
    </div>
  `).join('');
}

async function previewFile(category, name) {
  const res = await fetch(`${API}/api/files/${category}/${encodeURIComponent(name)}`);
  if (!res.ok) return;
  const text = await res.text();
  document.getElementById('preview-title').textContent = `${category}/${name}`;
  document.getElementById('preview-content').textContent = text;
  document.getElementById('file-preview').style.display = 'flex';
}

document.getElementById('btn-close-preview').addEventListener('click', () => {
  document.getElementById('file-preview').style.display = 'none';
});

async function deleteFile(category, name) {
  if (!confirm(`Delete ${category}/${name}?`)) return;
  await fetch(`${API}/api/files/${category}/${encodeURIComponent(name)}`, { method: 'DELETE' });
  loadFiles();
}

// "Use in run" prefills the edit modal with this filename so the user can
// quickly attach it to a run.
function useFileInRun(category, name) {
  // Switch to run modal with this file pre-filled.
  openModal(state.selectedID || null);
  if (category === 'configs') {
    document.getElementById('modal-config').value = name;
  } else {
    document.getElementById('modal-scenario').value = name;
  }
}

// File upload via input[type=file]
document.getElementById('file-upload-input').addEventListener('change', async (e) => {
  const files = Array.from(e.target.files);
  if (!files.length) return;

  for (const file of files) {
    const form = new FormData();
    form.append('file', file);
    const res = await fetch(`${API}/api/files/${currentFileCategory}`, {
      method: 'POST',
      body: form,
    });
    if (!res.ok) {
      const msg = await res.text();
      alert(`Upload failed for ${file.name}: ${msg}`);
    }
  }
  e.target.value = ''; // reset input
  loadFiles();
});

// Also support drag-and-drop onto the Files tab panel
const filesPanel = document.getElementById('tab-files');
filesPanel.addEventListener('dragover', (e) => {
  e.preventDefault();
  filesPanel.style.outline = '2px dashed var(--accent)';
});
filesPanel.addEventListener('dragleave', () => {
  filesPanel.style.outline = '';
});
filesPanel.addEventListener('drop', async (e) => {
  e.preventDefault();
  filesPanel.style.outline = '';
  const files = Array.from(e.dataTransfer.files).filter(f =>
    f.name.endsWith('.yaml') || f.name.endsWith('.yml')
  );
  for (const file of files) {
    const form = new FormData();
    form.append('file', file);
    await fetch(`${API}/api/files/${currentFileCategory}`, { method: 'POST', body: form });
  }
  loadFiles();
});

function formatBytes(b) {
  if (b < 1024) return `${b} B`;
  return `${(b / 1024).toFixed(1)} KB`;
}

function formatDate(iso) {
  if (!iso) return '';
  return new Date(iso).toLocaleDateString();
}

// ── Utilities ─────────────────────────────────────────────
function escapeHtml(str) {
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}
