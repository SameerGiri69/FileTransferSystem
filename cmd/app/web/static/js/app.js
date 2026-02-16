// ===== App State =====
const state = {
    devices: new Map(),
    transfers: new Map(),
    selectedFiles: [],
    selectedDevice: null,
    ws: null
};

// ===== DOM Elements =====
const elements = {
    devicesList: document.getElementById('devicesList'),
    deviceCount: document.getElementById('deviceCount'),
    noDevices: document.getElementById('noDevices'),
    dropzone: document.getElementById('dropzone'),
    dropzoneOverlay: document.getElementById('dropzoneOverlay'),
    fileInput: document.getElementById('fileInput'),
    browseBtn: document.getElementById('browseBtn'),
    selectedFiles: document.getElementById('selectedFiles'),
    filesList: document.getElementById('filesList'),
    clearFiles: document.getElementById('clearFiles'),
    targetDevice: document.getElementById('targetDevice'),
    sendBtn: document.getElementById('sendBtn'),
    transfersList: document.getElementById('transfersList'),
    noTransfers: document.getElementById('noTransfers'),
    historyList: document.getElementById('historyList'),
    noHistory: document.getElementById('noHistory'),
    transfersView: document.getElementById('transfersView'),
    historyView: document.getElementById('historyView')
};

// ===== Auth & Tabs =====
async function logout() {
    await fetch('/api/logout', { method: 'POST' });
    window.location.reload();
}

function switchTab(tab) {
    const tabs = document.querySelectorAll('.tab-btn');
    tabs.forEach(t => t.classList.remove('active'));

    if (tab === 'transfers') {
        tabs[0].classList.add('active');
        elements.transfersView.style.display = 'block';
        elements.historyView.style.display = 'none';
    } else {
        tabs[1].classList.add('active');
        elements.transfersView.style.display = 'none';
        elements.historyView.style.display = 'block';
        loadHistory();
    }
}

async function loadHistory() {
    try {
        const res = await fetch('/api/history');
        const history = await res.json();
        renderHistory(history);
    } catch (err) {
        console.error('Error fetching history:', err);
    }
}

function renderHistory(history) {
    elements.historyList.innerHTML = '';

    if (!history || history.length === 0) {
        elements.noHistory.style.display = 'flex';
        return;
    }

    elements.noHistory.style.display = 'none';

    // Sort by timestamp descending
    history.sort((a, b) => new Date(b.timestamp) - new Date(a.timestamp));

    history.forEach(item => {
        // Map history item to transfer format for card creation
        const transfer = {
            id: item.id,
            fileName: item.fileName,
            fileSize: item.fileSize,
            direction: item.direction,
            peerName: item.peerName,
            status: item.status,
            progress: 100,
            transferred: item.fileSize,
            startTime: item.timestamp
        };
        const card = createTransferCard(transfer, true);
        elements.historyList.appendChild(card);
    });
}


// ===== WebSocket Connection =====
function connectWebSocket() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    state.ws = new WebSocket(`${protocol}//${window.location.host}/ws`);

    state.ws.onopen = () => {
        console.log('WebSocket connected');
    };

    state.ws.onmessage = (event) => {
        const message = JSON.parse(event.data);
        handleWebSocketMessage(message);
    };

    state.ws.onclose = () => {
        console.log('WebSocket disconnected, reconnecting...');
        setTimeout(connectWebSocket, 2000);
    };

    state.ws.onerror = (error) => {
        console.error('WebSocket error:', error);
    };
}

function handleWebSocketMessage(message) {
    switch (message.type) {
        case 'deviceFound':
            addDevice(message.data);
            break;
        case 'deviceLost':
            removeDevice(message.data.id);
            break;
        case 'transferStarted':
            addTransfer(message.data);
            break;
        case 'transferUpdate':
            updateTransfer(message.data);
            break;
    }
}

// ===== Device Management =====
function addDevice(device) {
    state.devices.set(device.id, device);
    renderDevices();
    updateDeviceSelect();
}

function removeDevice(deviceId) {
    state.devices.delete(deviceId);
    renderDevices();
    updateDeviceSelect();
}

function renderDevices() {
    const count = state.devices.size;
    elements.deviceCount.textContent = count;

    if (count === 0) {
        elements.noDevices.style.display = 'flex';
        return;
    }

    elements.noDevices.style.display = 'none';

    // Clear existing device cards (but not the empty state)
    const existingCards = elements.devicesList.querySelectorAll('.device-card');
    existingCards.forEach(card => card.remove());

    // Add device cards
    state.devices.forEach((device, id) => {
        const card = createDeviceCard(device);
        elements.devicesList.appendChild(card);
    });
}

function createDeviceCard(device) {
    const card = document.createElement('div');
    card.className = 'device-card';
    card.dataset.id = device.id;

    const initial = device.name.charAt(0).toUpperCase();
    const userDisplay = device.username ? `<span style="font-size: 0.8rem; opacity: 0.8">User: ${escapeHtml(device.username)}</span>` : '';

    card.innerHTML = `
        <div class="device-avatar">${initial}</div>
        <div class="device-details">
            <div class="device-card-name">${escapeHtml(device.name)}</div>
            ${userDisplay}
            <div class="device-card-ip">${device.ip}</div>
        </div>
    `;

    card.addEventListener('click', () => {
        elements.targetDevice.value = device.id;
        state.selectedDevice = device.id;
        updateSendButton();

        // Visual feedback
        document.querySelectorAll('.device-card').forEach(c => c.classList.remove('selected'));
        card.classList.add('selected');
    });

    return card;
}

function updateDeviceSelect() {
    const select = elements.targetDevice;
    const currentValue = select.value;

    // Clear options except placeholder
    while (select.options.length > 1) {
        select.remove(1);
    }

    // Add device options
    state.devices.forEach((device, id) => {
        const option = document.createElement('option');
        option.value = id;
        const nameDisplay = device.username ? `${device.name} (${device.username})` : device.name;
        option.textContent = `${nameDisplay} (${device.ip})`;
        select.appendChild(option);
    });

    // Restore selection if still valid
    if (state.devices.has(currentValue)) {
        select.value = currentValue;
    }
}

// ===== File Selection =====
function initDropzone() {
    const dropzone = elements.dropzone;

    // Click to browse
    elements.browseBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        elements.fileInput.click();
    });

    dropzone.addEventListener('click', () => {
        elements.fileInput.click();
    });

    // File input change
    elements.fileInput.addEventListener('change', (e) => {
        addFiles(Array.from(e.target.files));
        e.target.value = ''; // Reset for same file selection
    });

    // Drag and drop
    dropzone.addEventListener('dragover', (e) => {
        e.preventDefault();
        dropzone.classList.add('dragover');
    });

    dropzone.addEventListener('dragleave', (e) => {
        e.preventDefault();
        dropzone.classList.remove('dragover');
    });

    dropzone.addEventListener('drop', (e) => {
        e.preventDefault();
        dropzone.classList.remove('dragover');
        addFiles(Array.from(e.dataTransfer.files));
    });

    // Clear files
    elements.clearFiles.addEventListener('click', clearFiles);

    // Device select change
    elements.targetDevice.addEventListener('change', (e) => {
        state.selectedDevice = e.target.value;
        updateSendButton();
    });

    // Send button
    elements.sendBtn.addEventListener('click', sendFiles);
}

function addFiles(files) {
    files.forEach(file => {
        // Avoid duplicates
        if (!state.selectedFiles.some(f => f.name === file.name && f.size === file.size)) {
            state.selectedFiles.push(file);
        }
    });
    renderSelectedFiles();
}

function removeFile(index) {
    state.selectedFiles.splice(index, 1);
    renderSelectedFiles();
}

function clearFiles() {
    state.selectedFiles = [];
    renderSelectedFiles();
}

function renderSelectedFiles() {
    if (state.selectedFiles.length === 0) {
        elements.selectedFiles.style.display = 'none';
        return;
    }

    elements.selectedFiles.style.display = 'block';
    elements.filesList.innerHTML = '';

    state.selectedFiles.forEach((file, index) => {
        const item = document.createElement('div');
        item.className = 'file-item';

        item.innerHTML = `
            <div class="file-icon">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M13 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V9z"></path>
                    <polyline points="13 2 13 9 20 9"></polyline>
                </svg>
            </div>
            <div class="file-info">
                <div class="file-name">${escapeHtml(file.name)}</div>
                <div class="file-size">${formatFileSize(file.size)}</div>
            </div>
            <button class="file-remove" data-index="${index}">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <line x1="18" y1="6" x2="6" y2="18"></line>
                    <line x1="6" y1="6" x2="18" y2="18"></line>
                </svg>
            </button>
        `;

        item.querySelector('.file-remove').addEventListener('click', () => removeFile(index));
        elements.filesList.appendChild(item);
    });

    updateSendButton();
}

function updateSendButton() {
    const hasFiles = state.selectedFiles.length > 0;
    const hasDevice = state.selectedDevice && state.selectedDevice !== '';
    elements.sendBtn.disabled = !(hasFiles && hasDevice);
}

async function sendFiles() {
    if (!state.selectedDevice || state.selectedFiles.length === 0) return;

    const deviceId = state.selectedDevice;

    for (const file of state.selectedFiles) {
        const formData = new FormData();
        formData.append('deviceId', deviceId);
        formData.append('file', file);

        try {
            await fetch('/api/upload', {
                method: 'POST',
                body: formData
            });
        } catch (error) {
            console.error('Upload error:', error);
        }
    }

    // Clear after sending
    clearFiles();
}

// ===== Transfer Management =====
function addTransfer(transfer) {
    state.transfers.set(transfer.id, transfer);
    renderTransfers();
}

function updateTransfer(transfer) {
    state.transfers.set(transfer.id, transfer);
    renderTransfers();
}

function renderTransfers() {
    if (state.transfers.size === 0) {
        elements.noTransfers.style.display = 'flex';
        return;
    }

    elements.noTransfers.style.display = 'none';

    // Clear existing transfer cards
    const existingCards = elements.transfersList.querySelectorAll('.transfer-card');
    existingCards.forEach(card => card.remove());

    // Sort by start time (newest first)
    const sortedTransfers = Array.from(state.transfers.values())
        .sort((a, b) => new Date(b.startTime) - new Date(a.startTime));

    sortedTransfers.forEach(transfer => {
        const card = createTransferCard(transfer);
        elements.transfersList.appendChild(card);
    });
}

function createTransferCard(transfer, isHistory = false) {
    const isCompletedReceive = transfer.status === 'completed' && transfer.direction === 'receive';
    let statusClass = transfer.status;

    if (isCompletedReceive) {
        statusClass = 'downloaded';
    }

    const card = document.createElement('div');
    card.className = `transfer-card ${statusClass}`;
    card.dataset.id = transfer.id;

    const isSend = transfer.direction === 'send';

    // Determine icon class
    let iconClass = isSend ? 'send' : 'receive';
    if (isCompletedReceive) iconClass = 'downloaded';

    const directionIcon = isSend ?
        '<polyline points="17 11 12 6 7 11"></polyline><line x1="12" y1="6" x2="12" y2="18"></line>' :
        '<polyline points="7 13 12 18 17 13"></polyline><line x1="12" y1="18" x2="12" y2="6"></line>';

    const progress = transfer.progress || 0;
    const speed = transfer.speed ? `${transfer.speed.toFixed(2)} MB/s` : '';
    const transferred = formatFileSize(transfer.transferred || 0);
    const total = formatFileSize(transfer.fileSize);

    // Add download button for completed incoming transfers
    const downloadBtn = isCompletedReceive ?
        `<a href="/dl/${encodeURIComponent(transfer.fileName)}" download="${escapeHtml(transfer.fileName)}" class="btn-download">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"></path>
                <polyline points="7 10 12 15 17 10"></polyline>
                <line x1="12" y1="15" x2="12" y2="3"></line>
            </svg>
            Save to Device
        </a>` : '';

    card.innerHTML = `
        <div class="transfer-header">
            <div class="transfer-icon ${iconClass}">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    ${directionIcon}
                </svg>
            </div>
            <div class="transfer-info">
                <div class="transfer-filename">${escapeHtml(transfer.fileName)}</div>
                <div class="transfer-meta">
                    <span class="transfer-direction">${transfer.direction}</span>
                    <span>•</span>
                    <span class="transfer-peer">${escapeHtml(transfer.peerName)}</span>
                    ${isHistory ? `<span>•</span><span>${new Date(transfer.startTime).toLocaleDateString()}</span>` : ''}
                </div>
            </div>
            ${downloadBtn}
        </div>
        ${!isHistory ? `
        <div class="transfer-progress">
            <div class="progress-bar">
                <div class="progress-fill" style="width: ${progress}%"></div>
            </div>
        </div>` : ''}
        <div class="transfer-stats">
            <span>${total}</span>
            <span class="transfer-speed">${speed}</span>
        </div>
    `;

    return card;
}

// ===== Utility Functions =====
function formatFileSize(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

// ===== Initialize App =====
document.addEventListener('DOMContentLoaded', () => {
    connectWebSocket();
    initDropzone();

    // Fetch initial data
    fetch('/api/devices')
        .then(res => res.json())
        .then(devices => {
            devices.forEach(device => addDevice(device));
        })
        .catch(err => console.error('Error fetching devices:', err));

    fetch('/api/transfers')
        .then(res => res.json())
        .then(transfers => {
            transfers.forEach(transfer => addTransfer(transfer));
        })
        .catch(err => console.error('Error fetching transfers:', err));
});
