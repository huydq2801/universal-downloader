/**
 * UniversalDownloader — Frontend App JS (Wails)
 * Giao tiếp với Go backend qua Wails bindings
 */

// Wails runtime bindings (tự động generate khi build)
const go = window.go?.main?.App || {};

// ===== STATE =====
let queueItems = [];
let historyItems = [];
let settings = {};
let currentPage = 'queue';
let wsConnection = null;

// ===== INIT =====
document.addEventListener('DOMContentLoaded', async () => {
  setupNavigation();
  setupUrlDownloader();
  await loadInitialData();
  connectWebSocket();
  setupWailsEvents();
  
  // Refresh queue mỗi 2 giây
  setInterval(refreshQueue, 2000);
});

// ===== NAVIGATION =====
function setupNavigation() {
  document.querySelectorAll('.nav-item').forEach(item => {
    item.addEventListener('click', (e) => {
      e.preventDefault();
      const page = item.dataset.page;
      navigateTo(page);
    });
  });
}

function navigateTo(page) {
  currentPage = page;
  
  document.querySelectorAll('.nav-item').forEach(i => i.classList.remove('active'));
  document.querySelector(`[data-page="${page}"]`)?.classList.add('active');
  
  document.querySelectorAll('.page').forEach(p => p.classList.remove('active'));
  document.getElementById(`page-${page}`)?.classList.add('active');
  
  if (page === 'history') loadHistory();
  if (page === 'settings') loadSettings();
}

// ===== LOAD INITIAL DATA =====
async function loadInitialData() {
  await Promise.all([
    refreshQueue(),
    checkTools(),
    loadVersion(),
  ]);
}

async function loadVersion() {
  try {
    const version = await window.go.main.App.GetVersion();
    document.getElementById('appVersion').textContent = 'v' + version;
  } catch {}
}

// ===== QUEUE =====
async function refreshQueue() {
  try {
    // Lấy từ Wails binding
    queueItems = await window.go.main.App.GetQueueItems() || [];
  } catch {
    // Fallback: lấy từ HTTP API
    try {
      const r = await fetch('http://localhost:7890/api/queue');
      const data = await r.json();
      queueItems = data.items || [];
    } catch { return; }
  }
  
  renderQueue();
  updateStats();
}

function renderQueue() {
  const listEl = document.getElementById('queueList');
  const emptyEl = document.getElementById('queueEmpty');
  
  // Xóa items cũ
  listEl.querySelectorAll('.dl-card').forEach(el => el.remove());
  
  if (queueItems.length === 0) {
    emptyEl.style.display = 'flex';
    return;
  }
  
  emptyEl.style.display = 'none';
  
  // Sort: downloading first, then pending, then others
  const sorted = [...queueItems].sort((a, b) => {
    const order = { downloading: 0, pending: 1, failed: 2, completed: 3, cancelled: 4 };
    return (order[a.status] ?? 5) - (order[b.status] ?? 5);
  });
  
  for (const item of sorted) {
    const card = createDownloadCard(item);
    listEl.appendChild(card);
  }
  
  // Update badge
  const activeCount = queueItems.filter(i => i.status === 'downloading' || i.status === 'pending').length;
  const badge = document.getElementById('queueBadge');
  if (activeCount > 0) {
    badge.style.display = 'block';
    badge.textContent = activeCount;
  } else {
    badge.style.display = 'none';
  }
}

function createDownloadCard(item) {
  const card = document.createElement('div');
  card.className = `dl-card status-${item.status}`;
  card.dataset.id = item.id;
  
  const typeEmoji = { video: '🎬', hls: '📡', dash: '📡', audio: '🎵', blob: '💾' }[item.request?.type] || '🎬';
  const statusLabels = {
    pending: 'Chờ tải',
    downloading: 'Đang tải',
    completed: 'Hoàn thành',
    failed: 'Thất bại',
    cancelled: 'Đã hủy',
  };
  
  const title = item.request?.title || extractTitleFromUrl(item.request?.url) || 'Video';
  const url = item.request?.url || '';
  const progress = item.progress || 0;
  const isActive = item.status === 'downloading';
  const isFailed = item.status === 'failed';
  const isCompleted = item.status === 'completed';
  
  card.innerHTML = `
    <div class="dl-card-header">
      <div class="dl-type-icon">${typeEmoji}</div>
      <div class="dl-info">
        <div class="dl-title" title="${esc(title)}">${esc(title)}</div>
        <div class="dl-url" title="${esc(url)}">${shortenUrl(url)}</div>
        <div class="dl-meta">
          <span class="status-badge status-${item.status}">
            ${isActive ? '<span class="spinner"></span>' : ''}
            ${statusLabels[item.status] || item.status}
          </span>
          ${item.request?.quality ? `<span style="font-size:11px;color:var(--text-muted)">${esc(item.request.quality)}</span>` : ''}
          ${item.request?.format ? `<span style="font-size:11px;color:var(--text-muted)">${esc(item.request.format)}</span>` : ''}
        </div>
      </div>
      <div class="dl-actions">
        ${isCompleted ? `
          <button class="action-btn" onclick="openFolder('${esc(item.savePath || '')}')">📁</button>
        ` : ''}
        ${isFailed ? `
          <button class="action-btn" onclick="retryDownload('${item.id}')" title="Thử lại">🔄</button>
        ` : ''}
        ${isActive ? `
          <button class="action-btn danger" onclick="cancelDownload('${item.id}')" title="Hủy">⏹</button>
        ` : `
          <button class="action-btn danger" onclick="removeDownload('${item.id}')" title="Xóa">🗑</button>
        `}
      </div>
    </div>
    
    ${isActive ? `
      <div>
        <div class="dl-progress-row">
          <div class="dl-progress-bar">
            <div class="dl-progress-fill" style="width: ${progress}%"></div>
          </div>
          <div class="dl-progress-text">${progress.toFixed(1)}%</div>
        </div>
        <div class="dl-speed-eta">
          ${item.speed ? `<span>⚡ ${esc(item.speed)}</span>` : ''}
          ${item.eta ? `<span>⏱ ${esc(item.eta)}</span>` : ''}
        </div>
      </div>
    ` : ''}
    
    ${isFailed && item.error ? `
      <div class="dl-error">❌ ${esc(item.error)}</div>
    ` : ''}
    
    ${isCompleted ? `
      <div style="font-size:11px;color:var(--success);margin-top:6px">✅ Đã tải xong • ${esc(item.savePath || 'Xem thư mục tải')}</div>
    ` : ''}
  `;
  
  return card;
}

function updateStats() {
  const counts = { downloading: 0, pending: 0, completed: 0, failed: 0 };
  for (const item of queueItems) {
    counts[item.status] = (counts[item.status] || 0) + 1;
  }
  
  document.getElementById('statDownloading').textContent = counts.downloading;
  document.getElementById('statPending').textContent = counts.pending;
  document.getElementById('statCompleted').textContent = counts.completed;
  document.getElementById('statFailed').textContent = counts.failed;
}

// ===== QUEUE ACTIONS =====
async function cancelDownload(id) {
  try {
    await window.go.main.App.CancelDownload(id);
    showToast('Đã hủy tải xuống', 'success');
  } catch {
    await fetch(`http://localhost:7890/api/queue/${id}`, { method: 'DELETE' });
  }
  refreshQueue();
}

async function retryDownload(id) {
  try {
    await window.go.main.App.RetryDownload(id);
    showToast('Đang thử lại...', 'success');
  } catch {
    await fetch(`http://localhost:7890/api/queue/${id}/retry`, { method: 'POST' });
  }
  refreshQueue();
}

async function removeDownload(id) {
  try {
    await window.go.main.App.RemoveDownload(id);
  } catch {
    await fetch(`http://localhost:7890/api/queue/${id}/remove`, { method: 'DELETE' });
  }
  refreshQueue();
}

async function openFolder(path) {
  if (!path) return;
  try {
    await window.go.main.App.OpenFolder(path);
  } catch {}
}

// Clear completed
document.getElementById('clearCompletedBtn')?.addEventListener('click', async () => {
  const completed = queueItems.filter(i => i.status === 'completed' || i.status === 'cancelled');
  for (const item of completed) await removeDownload(item.id);
  showToast(`Đã xóa ${completed.length} items`, 'success');
});

// ===== HISTORY =====
async function loadHistory() {
  try {
    historyItems = await window.go.main.App.GetDownloadList() || [];
  } catch {
    try {
      const r = await fetch('http://localhost:7890/api/history');
      const data = await r.json();
      historyItems = data.records || [];
    } catch { return; }
  }
  renderHistory();
}

function renderHistory() {
  const listEl = document.getElementById('historyList');
  listEl.innerHTML = '';
  
  if (historyItems.length === 0) {
    listEl.innerHTML = `
      <div class="empty-state">
        <div class="empty-illustration">
          <svg width="64" height="64" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1">
            <circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>
          </svg>
        </div>
        <h3>Chưa có lịch sử</h3>
        <p>Các file đã tải sẽ xuất hiện ở đây</p>
      </div>`;
    return;
  }
  
  for (const record of historyItems) {
    // Tương tự createDownloadCard nhưng cho history records
    const card = document.createElement('div');
    card.className = `dl-card status-${record.status}`;
    const typeEmoji = { video: '🎬', hls: '📡', dash: '📡', audio: '🎵' }[record.type] || '🎬';
    const date = new Date(record.createdAt).toLocaleString('vi-VN');
    
    card.innerHTML = `
      <div class="dl-card-header">
        <div class="dl-type-icon">${typeEmoji}</div>
        <div class="dl-info">
          <div class="dl-title">${esc(record.title || 'Không rõ tên')}</div>
          <div class="dl-url">${shortenUrl(record.url)}</div>
          <div class="dl-meta">
            <span class="status-badge status-${record.status}">${record.status}</span>
            <span style="font-size:11px;color:var(--text-muted)">${date}</span>
            ${record.fileSize ? `<span style="font-size:11px;color:var(--text-muted)">${formatSize(record.fileSize)}</span>` : ''}
          </div>
        </div>
        <div class="dl-actions">
          ${record.status === 'completed' ? `<button class="action-btn" onclick="openFolder('${esc(record.savePath || '')}')">📁</button>` : ''}
        </div>
      </div>
    `;
    listEl.appendChild(card);
  }
}

document.getElementById('clearHistoryBtn')?.addEventListener('click', async () => {
  if (!confirm('Xóa toàn bộ lịch sử tải?')) return;
  try {
    await fetch('http://localhost:7890/api/history', { method: 'DELETE' });
    showToast('Đã xóa lịch sử', 'success');
    loadHistory();
  } catch (e) {
    showToast('Lỗi: ' + e.message, 'error');
  }
});

// ===== SETTINGS =====
async function loadSettings() {
  try {
    settings = await window.go.main.App.GetSettings() || {};
  } catch {
    try {
      const r = await fetch('http://localhost:7890/api/settings');
      settings = await r.json();
    } catch { return; }
  }
  
  document.getElementById('saveDir').value = settings.saveDirectory || '';
  document.getElementById('defaultQuality').value = settings.defaultQuality || 'best';
  document.getElementById('defaultFormat').value = settings.defaultFormat || 'mp4';
  document.getElementById('maxConcurrent').value = settings.maxConcurrent || 3;
  document.getElementById('ytdlpPath').value = settings.ytdlpPath || '';
  document.getElementById('ffmpegPath').value = settings.ffmpegPath || '';
}

document.getElementById('saveSettingsBtn')?.addEventListener('click', async () => {
  const newSettings = {
    saveDirectory: document.getElementById('saveDir').value,
    defaultQuality: document.getElementById('defaultQuality').value,
    defaultFormat: document.getElementById('defaultFormat').value,
    maxConcurrent: parseInt(document.getElementById('maxConcurrent').value) || 3,
    ytdlpPath: document.getElementById('ytdlpPath').value,
    ffmpegPath: document.getElementById('ffmpegPath').value,
  };
  
  try {
    await window.go.main.App.SaveSettings(newSettings);
    showToast('✅ Đã lưu cài đặt', 'success');
  } catch {
    await fetch('http://localhost:7890/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(newSettings),
    });
    showToast('✅ Đã lưu cài đặt', 'success');
  }
});

document.getElementById('browseDirBtn')?.addEventListener('click', async () => {
  try {
    const path = await window.go.main.App.SelectFolder();
    if (path) document.getElementById('saveDir').value = path;
  } catch {
    showToast('Chọn thư mục không khả dụng trong chế độ này', 'error');
  }
});

// ===== CHECK TOOLS =====
async function checkTools() {
  try {
    const tools = await window.go.main.App.CheckTools();
    updateToolStatus('ytdlpCheck', tools.ytdlp, 'ytdlpStatus');
    updateToolStatus('ffmpegCheck', tools.ffmpeg, 'ffmpegStatus');
  } catch {}
}

function updateToolStatus(checkId, ok, statusId) {
  const checkEl = document.getElementById(checkId);
  const icon = checkEl?.querySelector('.check-icon');
  if (icon) icon.textContent = ok ? '✅' : '❌';
  checkEl?.classList.toggle('ok', ok);
  checkEl?.classList.toggle('fail', !ok);
  
  const statusEl = document.getElementById(statusId);
  if (statusEl) statusEl.textContent = ok ? '✅' : '❌';
}

// ===== SETUP DIRECT URL DOWNLOADER =====
function setupUrlDownloader() {
  // Nút dán từ Clipboard
  const pasteBtn = document.getElementById('pasteBtn');
  if (pasteBtn) {
    pasteBtn.addEventListener('click', async () => {
      try {
        const text = await navigator.clipboard.readText();
        const input = document.getElementById('mainUrlInput');
        if (input && text) {
          input.value = text.trim();
        }
      } catch (err) {
        showToast('Không thể đọc Clipboard. Hãy dán thủ công.', 'error');
      }
    });
  }

  // Nút Tải xuống nhanh
  const quickDownloadBtn = document.getElementById('quickDownloadBtn');
  if (quickDownloadBtn) {
    quickDownloadBtn.addEventListener('click', async () => {
      const urlInput = document.getElementById('mainUrlInput');
      const url = urlInput ? urlInput.value.trim() : '';
      if (!url) {
        showToast('Vui lòng nhập hoặc dán URL cần tải!', 'error');
        return;
      }

      quickDownloadBtn.disabled = true;
      quickDownloadBtn.innerHTML = `<span class="spinner"></span> Đang xử lý...`;

      const req = {
        url: url,
        quality: document.getElementById('quickQuality')?.value || 'best',
        format: document.getElementById('quickFormat')?.value || 'mp4',
        type: 'video',
        title: '',
        pageUrl: '',
      };

      try {
        try {
          await window.go.main.App.AddDownload(req);
        } catch {
          await fetch('http://localhost:7890/api/download', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(req),
          });
        }
        showToast('✅ Đã thêm vào hàng đợi tải!', 'success');
        if (urlInput) urlInput.value = '';
        refreshQueue();
      } catch (err) {
        showToast(`❌ Thất bại: ${err.message || 'Không thể kết nối backend'}`, 'error');
      } finally {
        quickDownloadBtn.disabled = false;
        quickDownloadBtn.innerHTML = `
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
            <polyline points="7 10 12 15 17 10"/>
            <line x1="12" y1="15" x2="12" y2="3"/>
          </svg>
          Tải xuống
        `;
      }
    });
  }

  // Phím Enter trong ô nhập
  const mainUrlInput = document.getElementById('mainUrlInput');
  if (mainUrlInput) {
    mainUrlInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        quickDownloadBtn?.click();
      }
    });
  }
}

// ===== WEBSOCKET =====
function connectWebSocket() {
  try {
    wsConnection = new WebSocket('ws://localhost:7890/ws');
    
    wsConnection.onmessage = (event) => {
      const msg = JSON.parse(event.data);
      if (msg.type === 'progress') {
        updateItemProgress(msg.data);
      }
    };
    
    wsConnection.onclose = () => {
      setTimeout(connectWebSocket, 3000);
    };
  } catch {}
}

function updateItemProgress(update) {
  const card = document.querySelector(`[data-id="${update.id}"]`);
  if (!card) return;
  
  const fill = card.querySelector('.dl-progress-fill');
  const text = card.querySelector('.dl-progress-text');
  if (fill) fill.style.width = `${update.progress}%`;
  if (text) text.textContent = `${update.progress.toFixed(1)}%`;
  
  // Nếu complete/failed, refresh full list
  if (update.status === 'completed' || update.status === 'failed') {
    setTimeout(refreshQueue, 500);
  }
}

// ===== WAILS EVENTS =====
function setupWailsEvents() {
  try {
    window.runtime?.EventsOn('download:progress', (update) => {
      updateItemProgress(update);
      if (update.status === 'completed' || update.status === 'failed') {
        setTimeout(refreshQueue, 500);
      }
    });
  } catch {}
}

// ===== UTILITIES =====
function extractTitleFromUrl(url) {
  try {
    const u = new URL(url || '');
    const host = u.hostname.replace('www.', '');
    // YouTube
    if (host === 'youtube.com' || host === 'youtu.be') {
      return 'YouTube Video';
    }
    // Facebook
    if (host.includes('facebook.com') || host === 'fb.watch') return 'Facebook Video';
    // TikTok
    if (host.includes('tiktok.com')) return 'TikTok Video';
    // Instagram
    if (host.includes('instagram.com')) return 'Instagram Video';
    // Twitter/X
    if (host.includes('twitter.com') || host === 'x.com') return 'X/Twitter Video';
    // Generic: lấy tên file từ path nhưng bỏ qua 'watch'
    const parts = u.pathname.split('/').filter(Boolean);
    const last = parts[parts.length - 1] || '';
    const clean = decodeURIComponent(last).replace(/\.[^.]+$/, '').replace(/[-_]/g, ' ');
    if (clean && clean !== 'watch' && clean.length > 1) return clean;
    return host;
  } catch {
    return 'Video';
  }
}

function shortenUrl(url) {
  if (!url) return '';
  try {
    const u = new URL(url);
    const path = u.pathname.length > 40 ? '...' + u.pathname.slice(-40) : u.pathname;
    return u.hostname + path;
  } catch {
    return url.slice(0, 60);
  }
}

function formatSize(bytes) {
  if (!bytes) return '';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return (bytes / Math.pow(k, i)).toFixed(1) + ' ' + sizes[i];
}

function esc(str) {
  return String(str || '')
    .replace(/&/g, '&amp;').replace(/</g, '&lt;')
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

let toastTimer;
function showToast(msg, type = '') {
  let toast = document.querySelector('.toast');
  if (!toast) {
    toast = document.createElement('div');
    toast.className = 'toast';
    document.body.appendChild(toast);
  }
  toast.textContent = msg;
  toast.className = `toast ${type}`;
  clearTimeout(toastTimer);
  requestAnimationFrame(() => {
    toast.classList.add('show');
    toastTimer = setTimeout(() => toast.classList.remove('show'), 3000);
  });
}
