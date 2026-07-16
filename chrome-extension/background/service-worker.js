/**
 * UniversalDownloader — Background Service Worker (v2)
 * 
 * Chiến lược phát hiện media:
 * 1. Bắt webRequest (HLS/DASH/MP4 trực tiếp từ network)
 * 2. Nhận thông báo từ content scripts (inject.js hook vào fetch/XHR/blob)
 * 3. Với YouTube/các trang phức tạp: gửi URL trang web sang backend (yt-dlp info)
 */

const BACKEND_URL = 'http://localhost:7890';

// Lưu media theo tabId
const tabMedia = new Map();

// ===== PATTERNS =====

const MEDIA_URL_PATTERNS = [
  /\.mp4(\?|$|#)/i,
  /\.webm(\?|$|#)/i,
  /\.mkv(\?|$|#)/i,
  /\.mov(\?|$|#)/i,
  /\.flv(\?|$|#)/i,
  /\.m3u8(\?|$|#)/i,
  /\.mpd(\?|$|#)/i,
  /\.mp3(\?|$|#)/i,
  /\.aac(\?|$|#)/i,
  /\.ogg(\?|$|#)/i,
  /\.wav(\?|$|#)/i,
  // YouTube streams (googlevideo)
  /googlevideo\.com\/videoplayback/i,
  // Facebook
  /fbcdn\.net.*\.(mp4|m3u8)/i,
  // Twitter/X
  /video\.twimg\.com/i,
  // Twitch
  /vod[\w-]*\.akamaized\.net/i,
  // Generic CDN video
  /\/(hls|dash|stream|video|media)\//i,
];

const MEDIA_CONTENT_TYPES = [
  'video/',
  'audio/',
  'application/x-mpegurl',
  'application/vnd.apple.mpegurl',
  'application/dash+xml',
];

// Các trang hỗ trợ yt-dlp (gửi page URL → backend lấy info)
const YTDLP_SUPPORTED_DOMAINS = [
  'youtube.com', 'youtu.be',
  'tiktok.com', 'vm.tiktok.com',
  'instagram.com',
  'facebook.com', 'fb.watch',
  'twitter.com', 'x.com',
  'twitch.tv',
  'vimeo.com',
  'dailymotion.com',
  'bilibili.com',
  'nicovideo.jp',
  'soundcloud.com',
  'reddit.com',
];

function isYtdlpPage(url) {
  try {
    const host = new URL(url).hostname.replace('www.', '');
    return YTDLP_SUPPORTED_DOMAINS.some(d => host === d || host.endsWith('.' + d));
  } catch { return false; }
}

function isMediaUrl(url, contentType = '') {
  if (MEDIA_URL_PATTERNS.some(p => p.test(url))) return true;
  if (MEDIA_CONTENT_TYPES.some(t => contentType.toLowerCase().startsWith(t))) return true;
  return false;
}

function classifyMedia(url, contentType = '') {
  if (/\.m3u8/i.test(url) || /mpegurl/i.test(contentType)) return 'hls';
  if (/\.mpd/i.test(url) || /dash\+xml/i.test(contentType)) return 'dash';
  if (/\.(mp3|aac|ogg|wav|flac)/i.test(url) || contentType.startsWith('audio/')) return 'audio';
  return 'video';
}

function extractQualityHint(url) {
  const m = url.match(/(\d{3,4})p/i);
  return m ? m[1] + 'p' : '';
}

// ===== TABS STORAGE =====

function getTabData(tabId) {
  if (!tabMedia.has(tabId)) {
    tabMedia.set(tabId, {
      mediaList: [],
      pageTitle: '',
      pageUrl: '',
      ytdlpChecked: false,
    });
  }
  return tabMedia.get(tabId);
}

function addMediaToTab(tabId, item) {
  const data = getTabData(tabId);

  // Lọc HLS segments
  if (item.type === 'hls-segment') return;

  // Dedup theo URL
  if (data.mediaList.some(m => m.url === item.url)) return;

  data.mediaList.push({
    id: Date.now() + Math.random(),
    timestamp: Date.now(),
    ...item,
  });

  updateBadge(tabId, data.mediaList.length);
}

function updateBadge(tabId, count) {
  if (count > 0) {
    chrome.action.setBadgeText({ text: count.toString(), tabId });
    chrome.action.setBadgeBackgroundColor({ color: '#6C63FF', tabId });
  } else {
    chrome.action.setBadgeText({ text: '', tabId });
  }
}

// ===== WEB REQUEST LISTENER =====

chrome.webRequest.onResponseStarted.addListener(
  (details) => {
    const { tabId, url, responseHeaders = [] } = details;
    if (tabId < 0) return;

    let contentType = '';
    let contentLength = 0;
    for (const h of responseHeaders) {
      const n = h.name.toLowerCase();
      if (n === 'content-type') contentType = h.value;
      if (n === 'content-length') contentLength = parseInt(h.value) || 0;
    }

    if (isMediaUrl(url, contentType)) {
      addMediaToTab(tabId, {
        url,
        type: classifyMedia(url, contentType),
        contentType,
        size: contentLength,
        source: 'network',
        quality: extractQualityHint(url),
      });
    }
  },
  { urls: ['<all_urls>'] },
  ['responseHeaders']
);

// ===== MESSAGE HANDLER =====

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  const tabId = sender.tab?.id;

  switch (message.type) {
    case 'MEDIA_FOUND': {
      if (tabId) {
        addMediaToTab(tabId, {
          url: message.url,
          type: message.mediaType || 'video',
          contentType: message.contentType || '',
          size: message.size || 0,
          source: message.source || 'page-hook',
          quality: message.quality || extractQualityHint(message.url || ''),
          title: message.title || '',
          thumbnail: message.thumbnail || '',
          blobData: message.blobData || null,
        });
      }
      sendResponse({ ok: true });
      break;
    }

    case 'GET_MEDIA_LIST': {
      // Lấy tab hiện tại để biết tabId thực
      chrome.tabs.query({ active: true, currentWindow: true }, (tabs) => {
        const activeTabId = tabs[0]?.id || tabId;
        const data = getTabData(activeTabId);
        sendResponse({
          mediaList: data.mediaList,
          pageTitle: data.pageTitle,
          pageUrl: data.pageUrl,
          ytdlpChecked: data.ytdlpChecked,
        });
      });
      return true; // async
    }

    case 'FETCH_YTDLP_INFO': {
      // Popup yêu cầu kiểm tra URL qua yt-dlp
      const { url } = message;
      fetchYtdlpInfo(url, tabId)
        .then(result => sendResponse({ ok: true, ...result }))
        .catch(err => sendResponse({ ok: false, error: err.message }));
      return true;
    }

    case 'CLEAR_MEDIA': {
      if (tabId) {
        tabMedia.delete(tabId);
        updateBadge(tabId, 0);
      }
      sendResponse({ ok: true });
      break;
    }

    case 'PAGE_INFO': {
      if (tabId) {
        const data = getTabData(tabId);
        data.pageTitle = message.title || data.pageTitle;
        data.pageUrl = message.url || data.pageUrl;
      }
      break;
    }

    case 'DOWNLOAD_REQUEST': {
      sendDownloadToBackend(message.item, message.options)
        .then(result => sendResponse({ ok: true, result }))
        .catch(err => sendResponse({ ok: false, error: err.message }));
      return true;
    }
  }

  return true;
});

// ===== YTDLP INFO =====

/**
 * Lấy thông tin media từ backend (yt-dlp)
 * Backend sẽ gọi yt-dlp --dump-json và trả về formats
 */
async function fetchYtdlpInfo(pageUrl, tabId) {
  const response = await fetch(`${BACKEND_URL}/api/info`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url: pageUrl }),
    signal: AbortSignal.timeout(20000),
  });

  if (!response.ok) {
    throw new Error(`yt-dlp info failed: ${response.status}`);
  }

  const info = await response.json();

  // Thêm vào media list của tab
  if (tabId && info) {
    // Thêm entry "tải toàn bộ" (best quality)
    addMediaToTab(tabId, {
      url: pageUrl,
      type: 'video',
      source: 'ytdlp',
      title: info.title || '',
      thumbnail: info.thumbnail || '',
      quality: 'best',
      formats: info.formats || [],
      duration: info.duration,
    });

    const data = getTabData(tabId);
    data.ytdlpInfo = info;
    data.ytdlpChecked = true;
  }

  return { info };
}

// ===== TAB EVENTS =====

chrome.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
  if (changeInfo.status === 'loading' && changeInfo.url) {
    // Xóa data cũ khi navigate
    tabMedia.delete(tabId);
    updateBadge(tabId, 0);
  }

  // Khi trang load xong, nếu là trang yt-dlp supported, tự động fetch info
  if (changeInfo.status === 'complete' && tab.url) {
    if (isYtdlpPage(tab.url) && !tab.url.includes('about:') && !tab.url.includes('chrome:')) {
      // Không auto-fetch để tránh spam, để người dùng bấm nút trong popup
    }
  }
});

chrome.tabs.onRemoved.addListener((tabId) => {
  tabMedia.delete(tabId);
});

// ===== BACKEND COMM =====

async function sendDownloadToBackend(item, options = {}) {
  const payload = {
    url: item.url,
    type: item.type || 'video',
    title: item.title || '',
    pageUrl: item.pageUrl || '',
    quality: options.quality || item.quality || 'best',
    format: options.format || 'mp4',
    headers: options.headers || {},
    blobData: item.blobData || null,
  };

  const r = await fetch(`${BACKEND_URL}/api/download`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });

  if (!r.ok) throw new Error(await r.text());
  return r.json();
}
