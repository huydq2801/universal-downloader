/**
 * UniversalDownloader — Popup v2
 * Hỗ trợ cả network-captured media và yt-dlp info lookup
 */

const BACKEND_URL = 'http://localhost:7890';

// === TỰ CONSTRUCT THUMBNAIL URL TỪ PAGE URL ===
function buildThumbnailFromPageUrl(pageUrl) {
  try {
    const u = new URL(pageUrl || '');
    const host = u.hostname.replace('www.', '');
    // YouTube
    if (host === 'youtube.com' || host === 'youtu.be') {
      const vid = host === 'youtu.be'
        ? u.pathname.slice(1)
        : u.searchParams.get('v');
      if (vid) return `https://img.youtube.com/vi/${vid}/hqdefault.jpg`;
    }
    return '';
  } catch { return ''; }
}

let mediaList = [];
let selectedIds = new Set();
let currentFilter = 'all';
let backendOnline = false;
let currentPageUrl = '';
let currentPageTitle = '';
let currentPageThumbnail = '';
let ytdlpChecked = false;
let isScanningYtdlp = false;

const $ = id => document.getElementById(id);

// ===== INIT =====
async function init() {
  await checkBackend();
  await loadMedia();
  setInterval(loadMedia, 3000);
  setInterval(checkBackend, 8000);
}

// ===== BACKEND CHECK =====
async function checkBackend() {
  try {
    const r = await fetch(`${BACKEND_URL}/api/status`, { signal: AbortSignal.timeout(2000) });
    backendOnline = r.ok;
  } catch {
    backendOnline = false;
  }
  const dot = $('statusDot');
  const txt = $('statusText');
  if (dot && txt) {
    dot.className = 'status-dot ' + (backendOnline ? 'online' : 'offline');
    txt.textContent = backendOnline ? 'Backend đang chạy • Sẵn sàng tải' : 'Backend chưa chạy — Mở UniversalDL';
  }
}

// ===== LOAD MEDIA =====
async function loadMedia() {
  return new Promise(resolve => {
    chrome.runtime.sendMessage({ type: 'GET_MEDIA_LIST' }, (resp) => {
      if (chrome.runtime.lastError || !resp) { resolve(); return; }
      mediaList = resp.mediaList || [];
      currentPageTitle = resp.pageTitle || '';
      currentPageUrl = resp.pageUrl || '';
      currentPageThumbnail = resp.pageThumbnail || '';
      ytdlpChecked = resp.ytdlpChecked || false;

      const el = $('pageTitle');
      if (el && currentPageTitle) { el.textContent = currentPageTitle; el.title = currentPageTitle; }

      updateTabCounts();
      renderMediaList();
      updateYtdlpBanner();
      resolve();
    });
  });
}

// ===== YTDLP BANNER =====
function updateYtdlpBanner() {
  const banner = $('ytdlpBanner');
  if (!banner) return;

  if (!currentPageUrl) { banner.style.display = 'none'; return; }

  const YTDLP_DOMAINS = [
    'youtube.com','youtu.be','tiktok.com','instagram.com','facebook.com',
    'fb.watch','twitter.com','x.com','twitch.tv','vimeo.com',
    'dailymotion.com','bilibili.com','reddit.com','soundcloud.com',
  ];
  let isSupported = false;
  try {
    const host = new URL(currentPageUrl).hostname.replace('www.', '');
    isSupported = YTDLP_DOMAINS.some(d => host === d || host.endsWith('.' + d));
  } catch {}

  if (isSupported && mediaList.filter(m => m.source === 'ytdlp').length === 0) {
    banner.style.display = 'flex';
    const btn = $('ytdlpScanBtn');
    if (btn) {
      btn.textContent = isScanningYtdlp ? '⏳ Đang quét...' : '🔍 Quét với yt-dlp';
      btn.disabled = isScanningYtdlp;
    }
  } else {
    banner.style.display = 'none';
  }
}

// ===== YTDLP SCAN =====
async function scanWithYtdlp() {
  if (!backendOnline) { showToast('❌ Backend chưa chạy!', 'error'); return; }
  if (!currentPageUrl) { showToast('❌ Không xác định được trang hiện tại', 'error'); return; }
  if (isScanningYtdlp) return;

  isScanningYtdlp = true;
  updateYtdlpBanner();
  showToast('🔍 Đang quét với yt-dlp...', '');

  try {
    // Gửi request tới backend để lấy info
    const r = await fetch(`${BACKEND_URL}/api/info`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: currentPageUrl }),
      signal: AbortSignal.timeout(25000),
    });

    if (!r.ok) throw new Error(`Lỗi backend: ${r.status}`);
    const info = await r.json();

    // Thêm vào media list trong background
    chrome.runtime.sendMessage({
      type: 'MEDIA_FOUND',
      url: currentPageUrl,
      mediaType: 'video',
      source: 'ytdlp',
      title: info.title || currentPageTitle,
      thumbnail: info.thumbnail || '',
      quality: 'best',
    });

    // Nếu có các formats cụ thể, thêm từng format
    if (info.formats && info.formats.length > 0) {
      // Nhóm theo height (quality)
      const seen = new Set();
      for (const fmt of info.formats) {
        if (!fmt.url) continue;
        if (seen.has(fmt.height)) continue;
        if (!fmt.height && !fmt.tbr) continue;
        seen.add(fmt.height);

        chrome.runtime.sendMessage({
          type: 'MEDIA_FOUND',
          url: fmt.url,
          mediaType: fmt.vcodec !== 'none' ? 'video' : 'audio',
          source: 'ytdlp-format',
          title: info.title || '',
          quality: fmt.height ? `${fmt.height}p` : '',
          contentType: '',
        });
      }
    }

    showToast(`✅ Tìm thấy: ${info.title || 'video'}`, 'success');
    await loadMedia();
  } catch (err) {
    showToast(`❌ Không quét được: ${err.message}`, 'error');
  } finally {
    isScanningYtdlp = false;
    updateYtdlpBanner();
  }
}

// ===== COUNTS =====
function updateTabCounts() {
  const counts = { all: mediaList.length, video: 0, hls: 0, audio: 0 };
  for (const m of mediaList) {
    if (m.type === 'video' || m.type === 'blob') counts.video++;
    else if (m.type === 'hls' || m.type === 'dash') counts.hls++;
    else if (m.type === 'audio') counts.audio++;
  }
  if ($('countAll')) $('countAll').textContent = counts.all;
  if ($('countVideo')) $('countVideo').textContent = counts.video;
  if ($('countHls')) $('countHls').textContent = counts.hls;
  if ($('countAudio')) $('countAudio').textContent = counts.audio;
}

// ===== RENDER =====
function getFiltered() {
  if (currentFilter === 'all') return mediaList;
  if (currentFilter === 'video') return mediaList.filter(m => m.type === 'video' || m.type === 'blob');
  if (currentFilter === 'hls') return mediaList.filter(m => m.type === 'hls' || m.type === 'dash');
  if (currentFilter === 'audio') return mediaList.filter(m => m.type === 'audio');
  return mediaList;
}

function renderMediaList() {
  const list = $('mediaList');
  const empty = $('emptyState');
  if (!list) return;

  list.querySelectorAll('.media-item').forEach(el => el.remove());
  const filtered = getFiltered();

  if (filtered.length === 0) {
    if (empty) empty.style.display = 'flex';
    return;
  }
  if (empty) empty.style.display = 'none';

  for (const item of filtered) {
    list.appendChild(createItemEl(item));
  }
}

const TYPE_ICONS = {
  video:  { emoji: '🎬', cls: 'type-video',  badge: 'badge-video',  label: 'VIDEO' },
  hls:    { emoji: '📡', cls: 'type-hls',    badge: 'badge-hls',    label: 'HLS' },
  dash:   { emoji: '📡', cls: 'type-dash',   badge: 'badge-dash',   label: 'DASH' },
  audio:  { emoji: '🎵', cls: 'type-audio',  badge: 'badge-audio',  label: 'AUDIO' },
  blob:   { emoji: '💾', cls: 'type-blob',   badge: 'badge-blob',   label: 'BLOB' },
};

function createItemEl(item) {
  const ti = TYPE_ICONS[item.type] || TYPE_ICONS.video;
  const isSelected = selectedIds.has(item.id);
  const title = item.title || smartTitle(item.url);
  const shortUrl = shortenUrl(item.url);
  const sizeStr = item.size ? fmtSize(item.size) : '';
  const isYtdlp = item.source === 'ytdlp' || item.source === 'ytdlp-format';

  // Thumbnail: item -> pageThumbnail từ background -> tự build từ URL
  const thumb =
    item.thumbnail ||
    currentPageThumbnail ||
    buildThumbnailFromPageUrl(currentPageUrl);
  const qualityLabel = item.qualityLabel || item.quality || '';

  const el = document.createElement('div');
  el.className = `media-item${isSelected ? ' selected' : ''}`;
  el.dataset.id = String(item.id);

  el.innerHTML = `
    <div class="item-check">
      <svg width="10" height="10" viewBox="0 0 12 12" fill="none" stroke="white" stroke-width="2.5">
        <polyline points="2 6 5 9 10 3"/>
      </svg>
    </div>
    <div class="item-thumb">
      ${thumb
        ? `<img src="${esc(thumb)}" alt="" class="thumb-img" onerror="this.parentElement.classList.add('thumb-error')">`
        : `<div class="thumb-placeholder">${ti.emoji}</div>`
      }
    </div>
    <div class="item-info">
      <div class="item-title" title="${esc(title)}">${esc(title)}</div>
      <div class="item-url" title="${esc(item.url)}">${esc(shortUrl)}</div>
      <div class="item-meta">
        <span class="badge ${ti.badge}">${ti.label}</span>
        ${qualityLabel ? `<span class="badge badge-quality">${esc(qualityLabel)}</span>` : ''}
        ${sizeStr ? `<span class="badge badge-size">${sizeStr}</span>` : ''}
        ${isYtdlp ? `<span class="badge badge-ytdlp">yt-dlp</span>` : ''}
      </div>
    </div>
    <div class="item-actions">
      <button class="quick-dl-btn" data-id="${item.id}" title="Tải nhanh">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
          <polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>
        </svg>
      </button>
    </div>`;

  el.addEventListener('click', e => {
    if (e.target.closest('.quick-dl-btn')) return;
    toggleSelect(item.id);
  });

  el.querySelector('.quick-dl-btn').addEventListener('click', e => {
    e.stopPropagation();
    downloadItem(item, { quality: $('qualitySelect')?.value || 'best' });
  });

  return el;
}

// ===== SELECTION =====
function toggleSelect(id) {
  if (selectedIds.has(id)) selectedIds.delete(id);
  else selectedIds.add(id);
  updateSelectionUI();
}

function updateSelectionUI() {
  const count = selectedIds.size;
  const el = $('selectedCount');
  if (el) el.textContent = `${count} đã chọn`;
  document.querySelectorAll('.media-item').forEach(el => {
    el.classList.toggle('selected', selectedIds.has(parseFloat(el.dataset.id)));
  });
  const bar = $('downloadBar');
  if (bar) bar.style.display = count > 0 ? 'flex' : 'none';
}

// ===== DOWNLOAD =====
async function downloadItem(item, options = {}) {
  if (!backendOnline) { showToast('❌ Backend chưa chạy!', 'error'); return; }

  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  const payload = {
    ...item,
    pageUrl: tab?.url || currentPageUrl,
    quality: options.quality || $('qualitySelect')?.value || 'best',
    format: $('formatSelect')?.value || 'mp4',
  };

  // Nếu là yt-dlp entry, dùng page URL thay vì stream URL (để yt-dlp tự lấy)
  if (item.source === 'ytdlp') {
    payload.url = currentPageUrl;
  }

  chrome.runtime.sendMessage({ type: 'DOWNLOAD_REQUEST', item: payload, options }, resp => {
    if (resp?.ok) showToast('✅ Đã thêm vào hàng đợi!', 'success');
    else showToast(`❌ ${resp?.error || 'Lỗi kết nối'}`, 'error');
  });
}

async function downloadSelected() {
  const selected = mediaList.filter(m => selectedIds.has(m.id));
  if (!selected.length) return;
  const btn = $('downloadBtn');
  if (btn) { btn.disabled = true; btn.textContent = 'Đang thêm...'; }

  for (const item of selected) {
    await downloadItem(item);
    await new Promise(r => setTimeout(r, 150));
  }
  selectedIds.clear();
  updateSelectionUI();
  if (btn) { btn.disabled = false; btn.textContent = 'Tải xuống'; }
}

// ===== EVENT LISTENERS =====
document.addEventListener('DOMContentLoaded', () => {
  // Tabs filter
  document.querySelectorAll('.tab').forEach(tab => {
    tab.addEventListener('click', () => {
      document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
      tab.classList.add('active');
      currentFilter = tab.dataset.filter;
      renderMediaList();
    });
  });

  $('downloadBtn')?.addEventListener('click', downloadSelected);

  $('refreshBtn')?.addEventListener('click', async () => {
    const btn = $('refreshBtn');
    if (btn) btn.style.opacity = '0.4';
    await loadMedia();
    if (btn) btn.style.opacity = '1';
  });

  $('openAppBtn')?.addEventListener('click', () => {
    fetch(`${BACKEND_URL}/api/open-window`, { method: 'POST' }).catch(() =>
      showToast('Mở ứng dụng UniversalDL', 'info')
    );
  });

  $('clearBtn')?.addEventListener('click', () => {
    chrome.runtime.sendMessage({ type: 'CLEAR_MEDIA' }, () => {
      mediaList = [];
      selectedIds.clear();
      ytdlpChecked = false;
      updateTabCounts();
      updateSelectionUI();
      renderMediaList();
      updateYtdlpBanner();
    });
  });

  $('ytdlpScanBtn')?.addEventListener('click', scanWithYtdlp);
});

// ===== UTILS =====
function smartTitle(url) {
  try {
    const u = new URL(url);
    // YouTube: lấy v= param
    if (u.hostname.includes('youtube.com') || u.hostname.includes('youtu.be')) {
      return 'YouTube Video';
    }
    // Lấy tên file từ path
    const parts = u.pathname.split('/').filter(Boolean);
    const last = parts[parts.length - 1] || '';
    const clean = decodeURIComponent(last).replace(/\.[^.]+$/, '').replace(/[-_]/g, ' ');
    if (clean && clean !== 'watch') return clean;
    return u.hostname;
  } catch { return 'Video'; }
}

function shortenUrl(url) {
  try {
    const u = new URL(url);
    const p = u.pathname.length > 38 ? '...' + u.pathname.slice(-38) : u.pathname;
    return u.hostname + p;
  } catch { return url.length > 50 ? url.slice(0, 50) + '...' : url; }
}

function fmtSize(bytes) {
  if (!bytes) return '';
  const k = 1024, s = ['B','KB','MB','GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return (bytes / Math.pow(k, i)).toFixed(1) + ' ' + s[i];
}

function esc(s) {
  return String(s || '')
    .replace(/&/g,'&amp;').replace(/</g,'&lt;')
    .replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

let _toastTimer;
function showToast(msg, type = '') {
  let el = document.querySelector('.toast');
  if (!el) { el = document.createElement('div'); el.className = 'toast'; document.body.appendChild(el); }
  el.textContent = msg;
  el.className = `toast ${type}`;
  clearTimeout(_toastTimer);
  requestAnimationFrame(() => {
    el.classList.add('show');
    _toastTimer = setTimeout(() => el.classList.remove('show'), 3000);
  });
}

init();
