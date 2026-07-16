/**
 * UniversalDownloader — Popup JavaScript
 * Quản lý UI popup, hiển thị media, xử lý tải xuống
 */

const BACKEND_URL = 'http://localhost:7890';

// State
let mediaList = [];
let selectedIds = new Set();
let currentFilter = 'all';
let backendOnline = false;

// DOM refs
const $ = id => document.getElementById(id);
const mediaListEl = $('mediaList');
const emptyState = $('emptyState');
const downloadBar = $('downloadBar');
const downloadBtn = $('downloadBtn');
const qualitySelect = $('qualitySelect');
const selectedCountEl = $('selectedCount');
const statusDot = $('statusDot');
const statusText = $('statusText');
const pageTitleEl = $('pageTitle');

// Type icons mapping
const TYPE_ICONS = {
  video: { emoji: '🎬', class: 'type-video', badge: 'badge-video', label: 'VIDEO' },
  hls: { emoji: '📡', class: 'type-hls', badge: 'badge-hls', label: 'HLS' },
  dash: { emoji: '📡', class: 'type-dash', badge: 'badge-dash', label: 'DASH' },
  audio: { emoji: '🎵', class: 'type-audio', badge: 'badge-audio', label: 'AUDIO' },
  blob: { emoji: '💾', class: 'type-blob', badge: 'badge-blob', label: 'BLOB' },
};

// ===== INIT =====

async function init() {
  await checkBackend();
  loadMediaFromBackground();
  
  // Auto-refresh mỗi 3 giây
  setInterval(loadMediaFromBackground, 3000);
  setInterval(checkBackend, 5000);
}

// ===== BACKEND STATUS =====

async function checkBackend() {
  try {
    const r = await fetch(`${BACKEND_URL}/api/status`, { signal: AbortSignal.timeout(2000) });
    if (r.ok) {
      backendOnline = true;
      statusDot.className = 'status-dot online';
      statusText.textContent = 'Backend đang chạy • Sẵn sàng tải';
    } else {
      throw new Error('Not OK');
    }
  } catch {
    backendOnline = false;
    statusDot.className = 'status-dot offline';
    statusText.textContent = 'Backend chưa chạy — Mở ứng dụng UniversalDL';
  }
}

// ===== LOAD MEDIA =====

async function loadMediaFromBackground() {
  return new Promise((resolve) => {
    chrome.runtime.sendMessage({ type: 'GET_MEDIA_LIST' }, (response) => {
      if (chrome.runtime.lastError || !response) {
        resolve();
        return;
      }
      
      mediaList = response.mediaList || [];
      
      if (response.pageTitle) {
        pageTitleEl.textContent = response.pageTitle;
        pageTitleEl.title = response.pageTitle;
      }
      
      // Cập nhật tab counts
      updateTabCounts();
      renderMediaList();
      resolve();
    });
  });
}

function updateTabCounts() {
  const counts = { all: mediaList.length, video: 0, hls: 0, audio: 0 };
  
  for (const m of mediaList) {
    if (m.type === 'video' || m.type === 'blob') counts.video++;
    else if (m.type === 'hls' || m.type === 'dash') counts.hls++;
    else if (m.type === 'audio') counts.audio++;
  }
  
  $('countAll').textContent = counts.all;
  $('countVideo').textContent = counts.video;
  $('countHls').textContent = counts.hls;
  $('countAudio').textContent = counts.audio;
}

// ===== RENDER =====

function getFilteredMedia() {
  if (currentFilter === 'all') return mediaList;
  if (currentFilter === 'video') return mediaList.filter(m => m.type === 'video' || m.type === 'blob');
  if (currentFilter === 'hls') return mediaList.filter(m => m.type === 'hls' || m.type === 'dash');
  if (currentFilter === 'audio') return mediaList.filter(m => m.type === 'audio');
  return mediaList;
}

function renderMediaList() {
  const filtered = getFilteredMedia();
  
  // Xóa items cũ (không xóa empty state)
  const existingItems = mediaListEl.querySelectorAll('.media-item');
  existingItems.forEach(el => el.remove());
  
  if (filtered.length === 0) {
    emptyState.style.display = 'flex';
    return;
  }
  
  emptyState.style.display = 'none';
  
  for (const item of filtered) {
    const el = createMediaItemEl(item);
    mediaListEl.appendChild(el);
  }
}

function createMediaItemEl(item) {
  const typeInfo = TYPE_ICONS[item.type] || TYPE_ICONS.video;
  const isSelected = selectedIds.has(item.id);
  const shortUrl = shortenUrl(item.url);
  const sizeStr = item.size ? formatSize(item.size) : '';
  const title = item.title || extractTitleFromUrl(item.url);
  
  const el = document.createElement('div');
  el.className = `media-item${isSelected ? ' selected' : ''}`;
  el.dataset.id = item.id;
  
  el.innerHTML = `
    <div class="item-check">
      <svg width="10" height="10" viewBox="0 0 12 12" fill="none" stroke="white" stroke-width="2.5">
        <polyline points="2 6 5 9 10 3"/>
      </svg>
    </div>
    <div class="item-type-icon ${typeInfo.class}">${typeInfo.emoji}</div>
    <div class="item-info">
      <div class="item-title" title="${escHtml(title)}">${escHtml(title)}</div>
      <div class="item-url" title="${escHtml(item.url)}">${escHtml(shortUrl)}</div>
      <div class="item-meta">
        <span class="badge ${typeInfo.badge}">${typeInfo.label}</span>
        ${item.quality ? `<span class="badge badge-quality">${escHtml(item.quality)}</span>` : ''}
        ${sizeStr ? `<span class="badge badge-size">${sizeStr}</span>` : ''}
        ${item.source === 'blob-hook' ? '<span class="badge badge-blob">BLOB</span>' : ''}
      </div>
      <div class="item-progress" id="progress-${item.id}" style="display:none">
        <div class="item-progress-bar" id="progressBar-${item.id}"></div>
      </div>
    </div>
    <div class="item-actions">
      <button class="quick-download-btn" data-id="${item.id}" title="Tải nhanh (chất lượng tốt nhất)">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
          <polyline points="7 10 12 15 17 10"/>
          <line x1="12" y1="15" x2="12" y2="3"/>
        </svg>
      </button>
    </div>
  `;
  
  // Click item = toggle select
  el.addEventListener('click', (e) => {
    if (e.target.closest('.quick-download-btn')) return;
    toggleSelect(item.id);
  });
  
  // Quick download button
  el.querySelector('.quick-download-btn').addEventListener('click', (e) => {
    e.stopPropagation();
    downloadItem(item, { quality: 'best' });
  });
  
  return el;
}

// ===== SELECTION =====

function toggleSelect(id) {
  if (selectedIds.has(id)) {
    selectedIds.delete(id);
  } else {
    selectedIds.add(id);
  }
  
  updateSelectionUI();
}

function updateSelectionUI() {
  const count = selectedIds.size;
  selectedCountEl.textContent = `${count} đã chọn`;
  
  // Update item classes
  document.querySelectorAll('.media-item').forEach(el => {
    const id = parseFloat(el.dataset.id);
    el.classList.toggle('selected', selectedIds.has(id));
  });
  
  // Show/hide download bar
  downloadBar.style.display = count > 0 ? 'flex' : 'none';
}

// ===== DOWNLOAD =====

async function downloadItem(item, options = {}) {
  if (!backendOnline) {
    showToast('❌ Backend chưa chạy! Mở ứng dụng UniversalDL trước.', 'error');
    return;
  }
  
  // Get current tab URL for context
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  
  const payload = {
    ...item,
    pageUrl: tab?.url || '',
    quality: options.quality || qualitySelect.value,
  };
  
  try {
    chrome.runtime.sendMessage({
      type: 'DOWNLOAD_REQUEST',
      item: payload,
      options,
    }, (response) => {
      if (response?.ok) {
        showToast(`✅ Đã thêm vào hàng đợi tải!`, 'success');
        // Show progress bar
        const progressEl = document.getElementById(`progress-${item.id}`);
        if (progressEl) progressEl.style.display = 'block';
      } else {
        showToast(`❌ Lỗi: ${response?.error || 'Không thể kết nối backend'}`, 'error');
      }
    });
  } catch (err) {
    showToast(`❌ ${err.message}`, 'error');
  }
}

async function downloadSelected() {
  const selected = mediaList.filter(m => selectedIds.has(m.id));
  if (selected.length === 0) return;
  
  downloadBtn.disabled = true;
  downloadBtn.innerHTML = `<span class="spinner"></span> Đang thêm...`;
  
  for (const item of selected) {
    await downloadItem(item);
    await sleep(200); // Tránh spam backend
  }
  
  selectedIds.clear();
  updateSelectionUI();
  
  downloadBtn.disabled = false;
  downloadBtn.innerHTML = `
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
      <polyline points="7 10 12 15 17 10"/>
      <line x1="12" y1="15" x2="12" y2="3"/>
    </svg>
    Tải xuống`;
}

// ===== EVENT LISTENERS =====

// Filter tabs
document.querySelectorAll('.tab').forEach(tab => {
  tab.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    tab.classList.add('active');
    currentFilter = tab.dataset.filter;
    renderMediaList();
  });
});

// Download button
downloadBtn.addEventListener('click', downloadSelected);

// Refresh button
$('refreshBtn').addEventListener('click', async () => {
  $('refreshBtn').style.opacity = '0.5';
  await loadMediaFromBackground();
  setTimeout(() => { $('refreshBtn').style.opacity = '1'; }, 500);
});

// Open app button
$('openAppBtn').addEventListener('click', () => {
  fetch(`${BACKEND_URL}/api/open-window`, { method: 'POST' }).catch(() => {
    showToast('Mở ứng dụng UniversalDL từ system tray', 'error');
  });
});

// Clear button
$('clearBtn').addEventListener('click', () => {
  chrome.runtime.sendMessage({ type: 'CLEAR_MEDIA' }, () => {
    mediaList = [];
    selectedIds.clear();
    updateTabCounts();
    updateSelectionUI();
    renderMediaList();
  });
});

// ===== UTILITIES =====

function shortenUrl(url) {
  try {
    const u = new URL(url);
    const path = u.pathname;
    if (path.length > 40) return '...' + path.slice(-40);
    return u.hostname + path;
  } catch {
    return url.length > 50 ? url.slice(0, 50) + '...' : url;
  }
}

function extractTitleFromUrl(url) {
  try {
    const u = new URL(url);
    const parts = u.pathname.split('/').filter(Boolean);
    const last = parts[parts.length - 1] || u.hostname;
    return decodeURIComponent(last).replace(/[-_]/g, ' ');
  } catch {
    return 'Video không rõ tên';
  }
}

function formatSize(bytes) {
  if (bytes === 0) return '';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return (bytes / Math.pow(k, i)).toFixed(1) + ' ' + sizes[i];
}

function escHtml(str) {
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function sleep(ms) {
  return new Promise(r => setTimeout(r, ms));
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

// ===== START =====
init();
