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
let appInitialized = false;

async function initApp() {
  if (appInitialized) return;
  appInitialized = true;
  
  setupNavigation();
  setupUrlDownloader();
  await loadInitialData();
  setupWailsEvents();
  
  // Refresh queue mỗi 2 giây
  setInterval(refreshQueue, 2000);
}

document.addEventListener('DOMContentLoaded', () => {
  // Lắng nghe sự kiện Wails đã sẵn sàng
  window.addEventListener('wails:ready', initApp);
  
  // Phòng hờ chạy debug hoặc Wails đã sẵn sàng trước DOMContentLoaded
  if (window.go) {
    initApp();
  }
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

async function refreshQueue() {
  try {
    if (window.go?.main?.App) {
      queueItems = await window.go.main.App.GetQueueItems() || [];
    }
  } catch (err) {
    console.error("Lỗi lấy hàng đợi:", err);
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
          <button class="action-btn btn-open-folder" title="Mở thư mục">📁</button>
        ` : ''}
        ${isFailed ? `
          <button class="action-btn btn-retry" title="Thử lại">🔄</button>
        ` : ''}
        ${isActive ? `
          <button class="action-btn danger btn-cancel" title="Hủy">⏹</button>
        ` : `
          <button class="action-btn danger btn-remove" title="Xóa">🗑</button>
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

  // Gán sự kiện trực tiếp để tránh lỗi thoát chuỗi đường dẫn (Windows Backslash escape error)
  card.querySelector('.btn-open-folder')?.addEventListener('click', (e) => {
    e.stopPropagation();
    openFolder(item.savePath);
  });
  card.querySelector('.btn-retry')?.addEventListener('click', (e) => {
    e.stopPropagation();
    retryDownload(item.id);
  });
  card.querySelector('.btn-cancel')?.addEventListener('click', (e) => {
    e.stopPropagation();
    cancelDownload(item.id);
  });
  card.querySelector('.btn-remove')?.addEventListener('click', (e) => {
    e.stopPropagation();
    removeDownload(item.id);
  });
  
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
    if (window.go?.main?.App) {
      await window.go.main.App.CancelDownload(id);
      showToast('Đã hủy tải xuống', 'success');
    }
  } catch (err) {
    showToast('Lỗi: ' + err.message, 'error');
  }
  refreshQueue();
}

async function retryDownload(id) {
  try {
    if (window.go?.main?.App) {
      await window.go.main.App.RetryDownload(id);
      showToast('Đang thử lại...', 'success');
    }
  } catch (err) {
    showToast('Lỗi: ' + err.message, 'error');
  }
  refreshQueue();
}

async function removeDownload(id) {
  try {
    if (window.go?.main?.App) {
      await window.go.main.App.RemoveDownload(id);
    }
  } catch (err) {
    console.error("Lỗi xóa download:", err);
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
    if (window.go?.main?.App) {
      historyItems = await window.go.main.App.GetDownloadList() || [];
    }
  } catch (err) {
    console.error("Lỗi lấy lịch sử:", err);
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
          ${record.status === 'completed' ? `<button class="action-btn btn-open-folder" title="Mở thư mục">📁</button>` : ''}
        </div>
      </div>
    `;

    card.querySelector('.btn-open-folder')?.addEventListener('click', (e) => {
      e.stopPropagation();
      openFolder(record.savePath);
    });

    listEl.appendChild(card);
  }
}

document.getElementById('clearHistoryBtn')?.addEventListener('click', async () => {
  if (!confirm('Xóa toàn bộ lịch sử tải?')) return;
  try {
    if (window.go?.main?.App) {
      // Hãy gọi API xóa lịch sử thông qua DB ở Backend. 
      // Đợi đã, Wails app.go không có binding trực tiếp cho ClearHistory, nhưng chúng ta có thể gọi qua db.
      // Để xem app.go có binding gì cho việc này không... 
      // Không có, nhưng DB của Wails là SQLite. Hãy kiểm tra nếu có API nào hoặc ta sẽ thêm một binding cho ClearHistory.
      // Hoặc ta có thể add một method ClearHistory() trong app.go.
      // Hãy tạm thời gọi RemoveDownload cho từng record trong historyItems để xóa sạch.
      for (const item of historyItems) {
        await window.go.main.App.RemoveDownload(item.id);
      }
      showToast('Đã xóa lịch sử', 'success');
      loadHistory();
    }
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
  document.getElementById('geminiAPIKey').value = settings.geminiAPIKey || '';
  document.getElementById('geminiModel').value = settings.geminiModel || 'gemini-3.1-flash-lite';
}

document.getElementById('saveSettingsBtn')?.addEventListener('click', async () => {
  const newSettings = {
    saveDirectory: document.getElementById('saveDir').value,
    defaultQuality: document.getElementById('defaultQuality').value,
    defaultFormat: document.getElementById('defaultFormat').value,
    maxConcurrent: parseInt(document.getElementById('maxConcurrent').value) || 3,
    ytdlpPath: document.getElementById('ytdlpPath').value,
    ffmpegPath: document.getElementById('ffmpegPath').value,
    geminiAPIKey: document.getElementById('geminiAPIKey').value.trim(),
    geminiModel: document.getElementById('geminiModel').value,
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

  // Nút đóng bảng phân tích
  document.getElementById('closeAnalysisBtn')?.addEventListener('click', () => {
    document.getElementById('analysisArea').style.display = 'none';
  });

  // Nút Phân tích / Tải xuống chính (Phân tích thường)
  const quickDownloadBtn = document.getElementById('quickDownloadBtn');
  if (quickDownloadBtn) {
    quickDownloadBtn.innerHTML = `
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
        <circle cx="11" cy="11" r="8"/>
        <line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      Phân tích
    `;

    quickDownloadBtn.addEventListener('click', async () => {
      const urlInput = document.getElementById('mainUrlInput');
      const url = urlInput ? urlInput.value.trim() : '';
      if (!url) {
        showToast('Vui lòng nhập hoặc dán URL trang web / video cần tải!', 'error');
        return;
      }

      quickDownloadBtn.disabled = true;
      quickDownloadBtn.innerHTML = `<span class="spinner"></span> Đang quét...`;
      
      const area = document.getElementById('analysisArea');
      area.style.display = 'block';
      document.getElementById('analysisTitle').textContent = 'Đang phân tích liên kết...';
      document.getElementById('analysisDomain').textContent = shortenUrl(url);
      document.getElementById('analysisThumb').src = '';
      document.getElementById('analysisDuration').textContent = '00:00';
      document.getElementById('formatsList').innerHTML = '<div style="padding: 20px; text-align: center; color: var(--text-muted)"><span class="spinner"></span> Đang tải các định dạng video khả dụng từ trang...</div>';

      try {
        const info = await window.go.main.App.GetInfo(url);
        if (!info) throw new Error('Không nhận được thông tin video.');
        renderAnalysisResult(url, info);
      } catch (err) {
        showToast(`❌ Phân tích thất bại: ${err.message || err}`, 'error');
        document.getElementById('formatsList').innerHTML = `<div style="padding: 20px; text-align: center; color: var(--error)">❌ Không thể phân tích link này. Thử dán link trực tiếp hơn hoặc tải lại.<br><small>${esc(err.message || err)}</small></div>`;
      } finally {
        quickDownloadBtn.disabled = false;
        quickDownloadBtn.innerHTML = `
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <circle cx="11" cy="11" r="8"/>
            <line x1="21" y1="21" x2="16.65" y2="16.65"/>
          </svg>
          Phân tích
        `;
      }
    });
  }

  // Nút ✨ Trích xuất AI (Gemini)
  const aiExtractBtn = document.getElementById('aiExtractBtn');
  if (aiExtractBtn) {
    aiExtractBtn.addEventListener('click', async () => {
      const urlInput = document.getElementById('mainUrlInput');
      const url = urlInput ? urlInput.value.trim() : '';
      if (!url) {
        showToast('Vui lòng nhập hoặc dán URL trang web cần dùng AI phân tích!', 'error');
        return;
      }

      // Kiểm tra API Key có được lưu chưa
      if (!settings || !settings.geminiAPIKey) {
        showToast('⚠️ Vui lòng cấu hình Gemini API Key trong Cài đặt trước!', 'error');
        navigateTo('settings');
        document.getElementById('geminiAPIKey')?.focus();
        return;
      }

      aiExtractBtn.disabled = true;
      aiExtractBtn.innerHTML = `<span class="spinner"></span> Đang quét bằng AI...`;
      
      const area = document.getElementById('analysisArea');
      area.style.display = 'block';
      document.getElementById('analysisTitle').textContent = 'Gemini AI đang tìm liên kết video ẩn...';
      document.getElementById('analysisDomain').textContent = shortenUrl(url);
      document.getElementById('analysisThumb').src = '';
      document.getElementById('analysisDuration').textContent = '--:--';
      document.getElementById('formatsList').innerHTML = '<div style="padding: 20px; text-align: center; color: var(--text-muted)"><span class="spinner"></span> Gemini AI đang đọc mã nguồn HTML và trích xuất các luồng stream video ẩn...</div>';

      try {
        const aiResult = await window.go.main.App.AnalyzeWithAI(url);
        if (!aiResult) throw new Error('AI không tìm thấy video nào.');
        renderAIAnalysisResult(url, aiResult);
      } catch (err) {
        showToast(`❌ AI trích xuất thất bại: ${err.message || err}`, 'error');
        document.getElementById('formatsList').innerHTML = `<div style="padding: 20px; text-align: center; color: var(--error)">❌ AI không thể trích xuất video từ trang này.<br><small>${esc(err.message || err)}</small></div>`;
      } finally {
        aiExtractBtn.disabled = false;
        aiExtractBtn.innerHTML = `✨ Trích xuất AI`;
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

// ===== RENDER KẾT QUẢ PHÂN TÍCH AI =====
function renderAIAnalysisResult(originalUrl, aiResult) {
  document.getElementById('analysisTitle').textContent = aiResult.title || 'Video trích xuất bằng AI';
  
  let domain = 'Website';
  try {
    domain = new URL(originalUrl).hostname.replace('www.', '');
  } catch {}
  document.getElementById('analysisDomain').textContent = domain + " (AI Extracted)";

  const thumbEl = document.getElementById('analysisThumb');
  if (aiResult.thumbnail) {
    thumbEl.src = aiResult.thumbnail;
    thumbEl.style.display = 'block';
  } else {
    thumbEl.style.display = 'none';
  }

  document.getElementById('analysisDuration').style.display = 'none'; // AI may not know exact duration

  const formatsList = document.getElementById('formatsList');
  formatsList.innerHTML = '';

  if (!aiResult.formats || aiResult.formats.length === 0) {
    formatsList.innerHTML = '<div style="padding: 20px; text-align: center; color: var(--warning)">⚠️ AI không tìm thấy link video trực tiếp nào khả dụng trên trang này.</div>';
    return;
  }

  for (const fmt of aiResult.formats) {
    const itemEl = document.createElement('div');
    itemEl.className = 'format-item';

    const isVideo = fmt.ext !== 'mp3' && fmt.ext !== 'm4a';
    const typeIcon = isVideo ? '🎬' : '🎵';
    const labelText = `${fmt.title || 'Đường dẫn video'} (${fmt.ext || 'mp4'})`;
    const descText = `Chất lượng: ${fmt.quality || 'Tự động'} • Trích xuất bởi Gemini`;

    itemEl.innerHTML = `
      <div class="format-info-col">
        <span class="format-icon">${typeIcon}</span>
        <div class="format-desc">
          <strong>${esc(labelText)}</strong>
          <span>${esc(descText)}</span>
          <span style="font-size: 10px; color: var(--accent2); word-break: break-all;">${esc(shortenUrl(fmt.URL))}</span>
        </div>
      </div>
      <button class="btn btn-primary btn-sm btn-dl-format">Tải về</button>
    `;

    itemEl.querySelector('.btn-dl-format').addEventListener('click', () => {
      downloadSelectedFormat(originalUrl, aiResult.title || fmt.title, fmt.URL, fmt.ext);
    });

    formatsList.appendChild(itemEl);
  }
}


// ===== RENDER KẾT QUẢ PHÂN TÍCH =====
function renderAnalysisResult(originalUrl, info) {
  document.getElementById('analysisTitle').textContent = info.title || 'Video';
  
  let domain = 'Website';
  try {
    domain = new URL(originalUrl).hostname.replace('www.', '');
  } catch {}
  document.getElementById('analysisDomain').textContent = domain;

  // Thumbnail
  const thumbEl = document.getElementById('analysisThumb');
  if (info.thumbnail) {
    thumbEl.src = info.thumbnail;
    thumbEl.style.display = 'block';
  } else {
    thumbEl.style.display = 'none';
  }

  // Duration
  const durationEl = document.getElementById('analysisDuration');
  if (info.duration > 0) {
    const mins = Math.floor(info.duration / 60);
    const secs = Math.floor(info.duration % 60);
    durationEl.textContent = `${mins}:${secs < 10 ? '0' : ''}${secs}`;
    durationEl.style.display = 'inline-block';
  } else {
    durationEl.style.display = 'none';
  }

  const formatsList = document.getElementById('formatsList');
  formatsList.innerHTML = '';

  // Nhóm các format phổ biến hoặc hiển thị định dạng tốt nhất trước
  // 1. Tải tốt nhất
  const bestRow = document.createElement('div');
  bestRow.className = 'format-item best-format';
  bestRow.innerHTML = `
    <div class="format-info-col">
      <span class="format-icon">🚀</span>
      <div class="format-desc">
        <strong>Tự động (Chất lượng cao nhất)</strong>
        <span>Tải ghép video + audio tốt nhất có sẵn</span>
      </div>
    </div>
    <button class="btn btn-primary btn-sm btn-dl-format">Tải xuống</button>
  `;
  bestRow.querySelector('.btn-dl-format').addEventListener('click', () => {
    downloadSelectedFormat(originalUrl, info.title, 'best', 'mp4');
  });
  formatsList.appendChild(bestRow);

  // 2. Tách các formats cụ thể
  if (info.formats && info.formats.length > 0) {
    // Sắp xếp format video từ cao xuống thấp và filter bớt trùng lặp
    const seenHeights = new Set();
    const sortedFormats = info.formats
      .filter(f => f.height || f.format_note)
      .sort((a, b) => (b.height || 0) - (a.height || 0));

    for (const fmt of sortedFormats) {
      // Chỉ hiển thị các format video chính hoặc audio chính để tránh làm rối UI
      const isVideo = fmt.vcodec !== 'none';
      const height = fmt.height;
      const sizeStr = fmt.filesize ? formatSize(fmt.filesize) : (fmt.tbr ? `~${Math.round(fmt.tbr)}kbps` : '');
      const formatLabel = fmt.format_note || (height ? `${height}p` : 'Video');
      
      // Dedup heights để gọn gàng
      if (isVideo && height) {
        const key = `${height}_${fmt.ext}`;
        if (seenHeights.has(key)) continue;
        seenHeights.add(key);
      }

      const itemEl = document.createElement('div');
      itemEl.className = 'format-item';
      
      const typeIcon = isVideo ? '🎬' : '🎵';
      const labelText = isVideo ? `${formatLabel} (${fmt.ext})` : `Audio (${fmt.ext})`;
      const descText = isVideo 
        ? `${fmt.width ? fmt.width + 'x' + fmt.height : ''} ${fmt.fps ? fmt.fps + 'fps' : ''} ${fmt.vcodec !== 'none' && fmt.acodec === 'none' ? '• Video only' : ''}` 
        : `Bitrate: ${Math.round(fmt.tbr || 0)}kbps`;

      itemEl.innerHTML = `
        <div class="format-info-col">
          <span class="format-icon">${typeIcon}</span>
          <div class="format-desc">
            <strong>${labelText}</strong>
            <span>${descText} ${sizeStr ? '• ' + sizeStr : ''}</span>
          </div>
        </div>
        <button class="btn btn-ghost btn-sm btn-dl-format">Tải về</button>
      `;

      itemEl.querySelector('.btn-dl-format').addEventListener('click', () => {
        // Nếu là video-only, dùng yt-dlp format selection để merge audio tự động
        let quality = fmt.format_id;
        if (fmt.vcodec !== 'none' && fmt.acodec === 'none') {
          // Kêu yt-dlp ghép format video này với audio tốt nhất
          quality = `${fmt.format_id}+bestaudio`;
        }
        downloadSelectedFormat(originalUrl, info.title, quality, fmt.ext);
      });

      formatsList.appendChild(itemEl);
    }
  }
}

// ===== THỰC HIỆN GỬI YÊU CẦU TẢI ĐÃ CHỌN =====
async function downloadSelectedFormat(originalUrl, videoTitle, qualityId, ext) {
  const req = {
    url: originalUrl,
    quality: qualityId, // format_id hoặc 'best'
    format: ext || 'mp4',
    type: ext === 'mp3' || ext === 'm4a' ? 'audio' : 'video',
    title: videoTitle,
    pageUrl: originalUrl,
  };

  try {
    await window.go.main.App.AddDownload(req);
    showToast('✅ Đã thêm vào hàng đợi tải xuống!', 'success');
    
    // Đóng panel và clear ô nhập
    document.getElementById('analysisArea').style.display = 'none';
    document.getElementById('mainUrlInput').value = '';
    
    // Switch to queue
    navigateTo('queue');
    refreshQueue();
  } catch (err) {
    showToast(`❌ Thất bại: ${err.message || err}`, 'error');
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
