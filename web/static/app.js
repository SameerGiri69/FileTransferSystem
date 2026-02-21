/* =============================================================
   FileTransfer — SPA JavaScript
   ============================================================= */

const App = (() => {
    let currentTab = 'scan';
    let selectedDeviceId = null;
    let selectedFile = null;
    let ws = null;
    let scanInterval = null;
    let activeTransfers = {};

    // ----------------------------------------------------------------
    // Init
    // ----------------------------------------------------------------
    async function init() {
        await loadMe();
        switchTab('scan');
        connectWS();
        scanDevices();
        scanInterval = setInterval(scanDevices, 4000);
    }

    async function loadMe() {
        try {
            const r = await fetch('/api/me');
            if (r.status === 401) { window.location.href = '/'; return; }
            const data = await r.json();
            document.getElementById('me-email').textContent = data.email;
        } catch (e) { /* ignore */ }
    }

    // ----------------------------------------------------------------
    // Tab routing
    // ----------------------------------------------------------------
    function switchTab(tab) {
        currentTab = tab;
        ['scan', 'downloads', 'history'].forEach(t => {
            document.getElementById(`tab-${t}`).classList.toggle('active', t === tab);
            document.getElementById(`content-${t}`).classList.toggle('active', t === tab);
        });
        if (tab === 'downloads') loadFiles();
        if (tab === 'history') loadHistory();
    }

    // ----------------------------------------------------------------
    // WebSocket
    // ----------------------------------------------------------------
    function connectWS() {
        const proto = location.protocol === 'https:' ? 'wss' : 'ws';
        ws = new WebSocket(`${proto}://${location.host}/ws`);

        ws.onmessage = (evt) => {
            try {
                const msg = JSON.parse(evt.data);
                handleWSMessage(msg);
            } catch (e) { }
        };

        ws.onclose = () => {
            setTimeout(connectWS, 2000); // auto-reconnect
        };
    }

    function handleWSMessage(msg) {
        const { type, payload } = msg;

        switch (type) {
            case 'incoming_request':
                showIncomingToast(payload);
                break;
            case 'transfer_update':
                updateActiveTransfer(payload);
                break;
            case 'transfer_rejected':
                removeActiveTransfer(payload.id);
                showFlash(`Transfer rejected: ${payload.fileName}`, 'error');
                break;
        }
    }

    // ----------------------------------------------------------------
    // Device Scan
    // ----------------------------------------------------------------
    async function scanDevices() {
        try {
            const r = await fetch('/api/devices');
            if (!r.ok) return;
            const devices = await r.json();
            renderDevices(devices);
        } catch (e) { }
    }

    function renderDevices(devices) {
        const grid = document.getElementById('device-grid');
        const empty = document.getElementById('scan-empty');

        if (!devices || devices.length === 0) {
            if (!document.getElementById('scan-empty')) {
                grid.innerHTML = '';
                const e = document.createElement('div');
                e.className = 'empty-state'; e.id = 'scan-empty';
                e.innerHTML = `<div class="empty-icon">📡</div>
          <div class="empty-title">Scanning for devices...</div>
          <div class="empty-sub">Make sure other devices are on the same Wi-Fi and signed in</div>`;
                grid.appendChild(e);
            }
            return;
        }

        grid.innerHTML = '';
        devices.forEach(dev => {
            const initial = (dev.username || dev.name || '?')[0].toUpperCase();
            const card = document.createElement('div');
            card.className = 'device-card';
            card.innerHTML = `
        <div class="device-avatar">${initial}</div>
        <div class="device-info">
          <div class="device-username">${esc(dev.username || 'Unknown')}</div>
          <div class="device-name">${esc(dev.name)}</div>
          <div class="device-ip">${esc(dev.ip)}:${dev.port}</div>
        </div>`;
            card.onclick = () => openSendDrawer(dev);
            grid.appendChild(card);
        });
    }

    // ----------------------------------------------------------------
    // Send Drawer
    // ----------------------------------------------------------------
    function openSendDrawer(device) {
        selectedDeviceId = device.id;
        selectedFile = null;

        const initial = (device.username || device.name || '?')[0].toUpperCase();
        document.getElementById('drawer-avatar').textContent = initial;
        document.getElementById('drawer-peer-name').textContent = device.username || device.name;
        document.getElementById('drawer-peer-ip').textContent = `${device.ip}:${device.port}`;
        document.getElementById('file-name-display').textContent = 'No file selected';
        document.getElementById('file-drop-zone').classList.remove('has-file');
        document.getElementById('send-btn').disabled = true;
        document.getElementById('file-input').value = '';

        document.getElementById('drawer-backdrop').classList.add('open');
        document.getElementById('send-drawer').classList.add('open');
    }

    function closeDrawer() {
        document.getElementById('drawer-backdrop').classList.remove('open');
        document.getElementById('send-drawer').classList.remove('open');
        selectedDeviceId = null;
        selectedFile = null;
    }

    function onFileSelect(evt) {
        const file = evt.target.files[0];
        if (!file) return;
        selectedFile = file;
        document.getElementById('file-name-display').textContent = `${file.name} (${fmtSize(file.size)})`;
        document.getElementById('file-drop-zone').classList.add('has-file');
        document.getElementById('send-btn').disabled = false;
    }

    async function doSend() {
        if (!selectedFile || !selectedDeviceId) return;

        const btn = document.getElementById('send-btn');
        btn.disabled = true;
        btn.textContent = 'Sending...';

        const fd = new FormData();
        fd.append('deviceId', selectedDeviceId);
        fd.append('file', selectedFile);

        try {
            const r = await fetch('/api/transfer/send', { method: 'POST', body: fd });
            let data;
            try {
                data = await r.json();
            } catch (e) {
                data = { error: `Server error (${r.status}): ${r.statusText}` };
            }

            if (!r.ok) {
                showFlash(data.error || 'Send failed', 'error');
            } else {
                showFlash('Transfer initiated! Waiting for receiver...', 'success');
                closeDrawer();
            }
        } catch (e) {
            console.error('Send error:', e);
            showFlash('Network error: ' + e.message, 'error');
        } finally {
            btn.disabled = false;
            btn.textContent = 'Send File';
        }
    }

    // ----------------------------------------------------------------
    // Active Transfers
    // ----------------------------------------------------------------
    function updateActiveTransfer(t) {
        activeTransfers[t.id] = t;
        renderActiveTransfers();
    }

    function removeActiveTransfer(id) {
        delete activeTransfers[id];
        renderActiveTransfers();
    }

    function renderActiveTransfers() {
        const list = document.getElementById('active-list');
        const section = document.getElementById('active-section');
        const items = Object.values(activeTransfers).filter(t => !['completed', 'failed', 'rejected'].includes(t.status));

        section.style.display = items.length ? 'block' : 'none';
        list.innerHTML = '';

        items.forEach(t => {
            const dirIcon = t.direction === 'send' ? '📤' : '📥';
            const pct = Math.round(t.progress || 0);
            const speed = t.speed ? `${t.speed.toFixed(1)} MB/s · ` : '';
            const row = document.createElement('div');
            row.className = 'transfer-row';
            row.innerHTML = `
        <div class="transfer-dir-icon">${dirIcon}</div>
        <div class="transfer-info">
          <div class="transfer-name">${esc(t.fileName)}</div>
          <div class="transfer-meta">${t.direction === 'send' ? 'To' : 'From'} ${esc(t.peerName)} · ${speed}${statusLabel(t.status)}</div>
        </div>
        <div class="transfer-progress-wrap">
          <div class="progress-bar-bg"><div class="progress-bar-fill" style="width:${pct}%"></div></div>
          <div class="progress-text">${pct}%</div>
        </div>`;
            list.appendChild(row);
        });
    }

    // ----------------------------------------------------------------
    // Incoming File Request Toast
    // ----------------------------------------------------------------
    function showIncomingToast(pt) {
        const container = document.getElementById('incoming-toast-container');
        const toast = document.createElement('div');
        toast.className = 'incoming-toast';
        toast.id = `toast-${pt.id}`;
        toast.innerHTML = `
      <div class="toast-header">
        <div class="toast-icon">📨</div>
        <div class="toast-title">Incoming File</div>
      </div>
      <div class="toast-body">
        <strong>${esc(pt.senderName)}</strong> wants to send you<br>
        <strong>${esc(pt.fileName)}</strong> (${fmtSize(pt.fileSize)})
      </div>
      <div class="toast-actions">
        <button class="btn-accept" onclick="App.acceptTransfer('${pt.id}')">✔ Accept</button>
        <button class="btn-reject" onclick="App.rejectTransfer('${pt.id}')">✕ Reject</button>
      </div>`;
        container.appendChild(toast);

        // Auto-dismiss after 2 min
        setTimeout(() => toast.remove(), 120000);
    }

    function dismissToast(id) {
        const el = document.getElementById(`toast-${id}`);
        if (el) el.remove();
    }

    async function acceptTransfer(id) {
        dismissToast(id);
        try {
            const r = await fetch('/api/transfer/accept', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ transferId: id })
            });
            if (r.ok) showFlash('Accepted — receiving file...', 'success');
            else {
                const d = await r.json();
                showFlash(d.error || 'Accept failed', 'error');
            }
        } catch (e) {
            showFlash('Network error', 'error');
        }
    }

    async function rejectTransfer(id) {
        dismissToast(id);
        try {
            await fetch('/api/transfer/reject', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ transferId: id })
            });
        } catch (e) { }
        showFlash('Transfer rejected', 'error');
    }

    // ----------------------------------------------------------------
    // Downloads Tab
    // ----------------------------------------------------------------
    async function loadFiles() {
        try {
            const r = await fetch('/api/files');
            if (!r.ok) return;
            const files = await r.json();
            renderFiles(files);
        } catch (e) { }
    }

    function renderFiles(files) {
        const list = document.getElementById('files-list');
        if (!files || files.length === 0) {
            list.innerHTML = `<div class="empty-state">
        <div class="empty-icon">📂</div>
        <div class="empty-title">No files yet</div>
        <div class="empty-sub">Files you receive will appear here</div>
      </div>`;
            return;
        }
        list.innerHTML = '';
        files.forEach(f => {
            const ext = f.name.split('.').pop().toUpperCase();
            const card = document.createElement('div');
            card.className = 'file-card';
            card.innerHTML = `
        <div class="file-icon">${fileIcon(f.name)}</div>
        <div class="file-meta">
          <div class="file-name">${esc(f.name)}</div>
          <div class="file-sub">${fmtSize(f.size)} · ${fmtTime(f.timestamp)}</div>
        </div>
        <a class="btn-dl" href="/dl/${encodeURIComponent(f.name)}" download="${esc(f.name)}">⬇ Download</a>`;
            list.appendChild(card);
        });

        // Update badge
        const badge = document.getElementById('downloads-badge');
        badge.textContent = files.length;
        badge.style.display = files.length ? 'flex' : 'none';
    }

    // ----------------------------------------------------------------
    // History Tab
    // ----------------------------------------------------------------
    async function loadHistory() {
        try {
            const r = await fetch('/api/history');
            if (!r.ok) return;
            const history = await r.json();
            renderHistory(history);
        } catch (e) { }
    }

    function renderHistory(history) {
        const wrap = document.getElementById('history-table-wrap');
        if (!history || history.length === 0) {
            wrap.innerHTML = `<div class="empty-state">
        <div class="empty-icon">🕒</div>
        <div class="empty-title">No history yet</div>
        <div class="empty-sub">Completed transfers will appear here</div>
      </div>`;
            return;
        }

        wrap.innerHTML = `
      <table>
        <thead>
          <tr>
            <th>File</th>
            <th>Direction</th>
            <th>Peer</th>
            <th>Size</th>
            <th>Time</th>
            <th>Status</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody id="history-body"></tbody>
      </table>`;

        const tbody = document.getElementById('history-body');
        history.forEach(item => {
            const dir = item.direction === 'send'
                ? '<span style="color:#a78bfa">↑ Sent</span>'
                : '<span style="color:#34d399">↓ Received</span>';
            const tr = document.createElement('tr');
            tr.innerHTML = `
         <td class="file-col">${esc(item.fileName)}</td>
        <td>${dir}</td>
        <td>${esc(item.peerName)}</td>
        <td>${fmtSize(item.fileSize)}</td>
        <td>${fmtTime(item.timestamp)}</td>
        <td><span class="status-badge status-${item.status}">${item.status}</span></td>
        <td>
          ${item.direction === 'receive' && item.status === 'completed'
                    ? `<a class="btn-dl-sm" href="/dl/${encodeURIComponent(item.fileName)}" download="${esc(item.fileName)}">⬇ Download</a>`
                    : ''}
        </td>`;
            tbody.appendChild(tr);
        });
    }

    // ----------------------------------------------------------------
    // Auth
    // ----------------------------------------------------------------
    async function logout() {
        await fetch('/api/auth/logout', { method: 'POST' });
        window.location.href = '/';
    }

    // ----------------------------------------------------------------
    // Flash notifications
    // ----------------------------------------------------------------
    function showFlash(msg, type) {
        const id = 'flash-' + Date.now();
        const el = document.createElement('div');
        el.id = id;
        el.style.cssText = `
      position:fixed; top:${64 + 8}px; left:50%; transform:translateX(-50%);
      z-index:1000; padding:12px 20px; border-radius:10px; font-size:14px; font-weight:500;
      background:${type === 'success' ? 'rgba(52,211,153,0.15)' : 'rgba(248,113,113,0.15)'};
      border:1px solid ${type === 'success' ? 'rgba(52,211,153,0.3)' : 'rgba(248,113,113,0.3)'};
      color:${type === 'success' ? '#34d399' : '#f87171'};
      animation: slide-down 0.3s ease; font-family:inherit;
    `;
        el.textContent = msg;
        document.body.appendChild(el);
        setTimeout(() => el.remove(), 3500);
    }

    // ----------------------------------------------------------------
    // Helpers
    // ----------------------------------------------------------------
    function fmtSize(bytes) {
        if (!bytes) return '0 B';
        const units = ['B', 'KB', 'MB', 'GB'];
        let i = 0;
        while (bytes >= 1024 && i < units.length - 1) { bytes /= 1024; i++; }
        return `${bytes.toFixed(i ? 1 : 0)} ${units[i]}`;
    }

    function fmtTime(ts) {
        if (!ts) return '';
        const d = new Date(ts);
        return d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
    }

    function esc(str) {
        if (!str) return '';
        return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
    }

    function fileIcon(name) {
        const ext = (name || '').split('.').pop().toLowerCase();
        const map = {
            jpg: '🖼️', jpeg: '🖼️', png: '🖼️', gif: '🖼️', svg: '🖼️', webp: '🖼️',
            pdf: '📄', doc: '📝', docx: '📝', txt: '📝', md: '📝',
            mp4: '🎬', mkv: '🎬', avi: '🎬', mov: '🎬',
            mp3: '🎵', wav: '🎵', flac: '🎵',
            zip: '📦', rar: '📦', tar: '📦', gz: '📦',
            exe: '⚙️', dmg: '⚙️', sh: '⚙️', py: '🐍', js: '📜', go: '🔹'
        };
        return map[ext] || '📄';
    }

    function statusLabel(s) {
        const map = { 'waiting_acceptance': '⏳ Awaiting acceptance', 'sending': '📤 Sending', 'receiving': '📥 Receiving', 'completed': '✔ Done', 'failed': '✘ Failed', 'rejected': '✘ Rejected' };
        return map[s] || s;
    }

    // Drag and drop on file zone
    document.addEventListener('DOMContentLoaded', () => {
        const zone = document.getElementById('file-drop-zone');
        if (!zone) return;
        zone.addEventListener('dragover', e => { e.preventDefault(); zone.classList.add('has-file'); });
        zone.addEventListener('dragleave', () => { if (!selectedFile) zone.classList.remove('has-file'); });
        zone.addEventListener('drop', e => {
            e.preventDefault();
            const file = e.dataTransfer.files[0];
            if (!file) return;
            selectedFile = file;
            document.getElementById('file-name-display').textContent = `${file.name} (${fmtSize(file.size)})`;
            zone.classList.add('has-file');
            document.getElementById('send-btn').disabled = false;
        });
    });

    return { init, switchTab, scanDevices, openSendDrawer, closeDrawer, onFileSelect, doSend, acceptTransfer, rejectTransfer, logout };
})();

// Kick off on load
document.addEventListener('DOMContentLoaded', App.init);

// Add slide-down keyframe to head dynamically
const st = document.createElement('style');
st.textContent = `@keyframes slide-down { from { opacity:0; transform:translateX(-50%) translateY(-10px); } to { opacity:1; transform:translateX(-50%) translateY(0); } }`;
document.head.appendChild(st);
