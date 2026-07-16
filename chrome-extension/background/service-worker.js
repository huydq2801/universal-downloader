/**
 * UniversalDownloader — Background Service Worker
 * 
 * Vai trò:
 * - Bắt tất cả network requests để tìm media URLs
 * - Nhận thông báo từ content scripts (blob, MediaSource)
 * - Giao tiếp với Go backend qua HTTP
 * - Lưu trữ danh sách media theo tab
 */

const BACKEND_URL = 'http://localhost:7890';

// Lưu media theo tabId: { tabId: { url, title, mediaList: [...] } }
const tabMedia = new Map();

// ===== NETWORK REQUEST INTERCEPTION =====

// Các pattern URL của media phổ biến
const MEDIA_PATTERNS = [
  /\.mp4(\?|$)/i,
  /\.webm(\?|$)/i,
  /\.mkv(\?|$)/i,
  /\.avi(\?|$)/i,
  /\.mov(\?|$)/i,
  /\.flv(\?|$)/i,
  /\.m3u8(\?|$)/i,       // HLS playlist
  /\.mpd(\?|$)/i,        // DASH manifest
  /\.ts(\?|$)/i,         // HLS segment
  /\.mp3(\?|$)/i,
  /\.aac(\?|$)/i,
  /\.ogg(\?|$)/i,
  /\.wav(\?|$)/i,
  /googlevideo\.com/i,   // YouTube video streams
  /fbcdn\.net.*\.mp4/i,  // Facebook videos
  /video\.twimg\.com/i,  // Twitter videos
  /vod.*\.akamaized\.net/i, // Twitch VOD
];

// Content-Type của media
const MEDIA_CONTENT_TYPES = [
  'video/',
  'audio/',
  'application/x-mpegURL',
  'application/vnd.apple.mpegurl',
  'application/dash+xml',
  'application/octet-stream', // Có thể là video
];

/**
 * Kiểm tra URL có phải media không
 */
function isMediaUrl(url, contentType = '') {
  const urlMatch = MEDIA_PATTERNS.some(p => p.test(url));
  const typeMatch = MEDIA_CONTENT_TYPES.some(t => contentType.toLowerCase().startsWith(t));
  return urlMatch || typeMatch;
}

/**
 * Phân loại loại media
 */
function classifyMedia(url, contentType = '', size = 0) {
  if (/\.m3u8/i.test(url) || /mpegURL/i.test(contentType)) return 'hls';
  if (/\.mpd/i.test(url) || /dash\+xml/i.test(contentType)) return 'dash';
  if (/\.(mp3|aac|ogg|wav|flac)/i.test(url) || contentType.startsWith('audio/')) return 'audio';
  if (/\.ts(\?|$)/i.test(url)) return 'hls-segment';
  return 'video';
}

/**
 * Thêm media item vào danh sách của tab
 */
function addMediaToTab(tabId, mediaItem) {
  if (!tabMedia.has(tabId)) {
    tabMedia.set(tabId, { mediaList: [], pageTitle: '', pageUrl: '' });
  }
  
  const tabData = tabMedia.get(tabId);
  
  // Bỏ qua HLS segments (chỉ lấy playlist)
  if (mediaItem.type === 'hls-segment') return;
  
  // Kiểm tra duplicate URL
  const exists = tabData.mediaList.some(m => m.url === mediaItem.url);
  if (!exists) {
    tabData.mediaList.push({
      id: Date.now() + Math.random(),
      ...mediaItem,
      timestamp: Date.now(),
    });
    
    // Cập nhật badge count
    const count = tabData.mediaList.length;
    chrome.action.setBadgeText({ text: count.toString(), tabId });
    chrome.action.setBadgeBackgroundColor({ color: '#6C63FF', tabId });
  }
}

// ===== WEB REQUEST LISTENER =====

chrome.webRequest.onResponseStarted.addListener(
  (details) => {
    const { tabId, url, responseHeaders = [] } = details;
    if (tabId < 0) return; // Bỏ qua requests không từ tab
    
    // Lấy content-type và content-length từ headers
    let contentType = '';
    let contentLength = 0;
    for (const header of responseHeaders) {
      const name = header.name.toLowerCase();
      if (name === 'content-type') contentType = header.value;
      if (name === 'content-length') contentLength = parseInt(header.value) || 0;
    }
    
    if (isMediaUrl(url, contentType)) {
      const type = classifyMedia(url, contentType, contentLength);
      
      addMediaToTab(tabId, {
        url,
        type,
        contentType,
        size: contentLength,
        source: 'network',
        quality: extractQualityFromUrl(url),
      });
    }
  },
  { urls: ['<all_urls>'] },
  ['responseHeaders']
);

/**
 * Cố gắng trích xuất chất lượng từ URL (ví dụ: 1080p, 720p)
 */
function extractQualityFromUrl(url) {
  const match = url.match(/(\d{3,4})p/i);
  return match ? match[1] + 'p' : '';
}

// ===== MESSAGES FROM CONTENT SCRIPTS =====

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  const tabId = sender.tab?.id;
  
  switch (message.type) {
    case 'MEDIA_FOUND': {
      // Content script tìm thấy media (fetch/XHR/blob/MediaSource)
      if (tabId) {
        addMediaToTab(tabId, {
          url: message.url,
          type: message.mediaType || 'video',
          contentType: message.contentType || '',
          size: message.size || 0,
          source: message.source || 'content-script',
          quality: message.quality || '',
          title: message.title || '',
          thumbnail: message.thumbnail || '',
          blobData: message.blobData || null, // base64 nếu là blob
        });
      }
      sendResponse({ ok: true });
      break;
    }
    
    case 'GET_MEDIA_LIST': {
      // Popup hỏi danh sách media của tab hiện tại
      const data = tabMedia.get(tabId) || { mediaList: [], pageTitle: '', pageUrl: '' };
      sendResponse({ mediaList: data.mediaList, pageTitle: data.pageTitle, pageUrl: data.pageUrl });
      break;
    }
    
    case 'CLEAR_MEDIA': {
      tabMedia.delete(tabId);
      chrome.action.setBadgeText({ text: '', tabId });
      sendResponse({ ok: true });
      break;
    }
    
    case 'DOWNLOAD_REQUEST': {
      // Popup yêu cầu tải — gửi tới Go backend
      sendDownloadToBackend(message.item, message.options)
        .then(result => sendResponse({ ok: true, result }))
        .catch(err => sendResponse({ ok: false, error: err.message }));
      return true; // async response
    }
    
    case 'PAGE_INFO': {
      // Content script gửi info về trang
      if (tabId) {
        const data = tabMedia.get(tabId) || { mediaList: [] };
        data.pageTitle = message.title;
        data.pageUrl = message.url;
        tabMedia.set(tabId, data);
      }
      break;
    }
  }
  
  return true;
});

// ===== TAB EVENTS =====

// Xóa data khi tab navigate sang trang mới
chrome.tabs.onUpdated.addListener((tabId, changeInfo) => {
  if (changeInfo.status === 'loading' && changeInfo.url) {
    tabMedia.delete(tabId);
    chrome.action.setBadgeText({ text: '', tabId });
  }
});

// Xóa data khi tab đóng
chrome.tabs.onRemoved.addListener((tabId) => {
  tabMedia.delete(tabId);
});

// ===== BACKEND COMMUNICATION =====

/**
 * Gửi yêu cầu tải xuống Go backend
 */
async function sendDownloadToBackend(item, options = {}) {
  const payload = {
    url: item.url,
    type: item.type,
    title: item.title || '',
    pageUrl: item.pageUrl || '',
    quality: options.quality || 'best',
    format: options.format || 'mp4',
    savePath: options.savePath || '',
    cookies: options.cookies || '',
    headers: options.headers || {},
    blobData: item.blobData || null,
  };
  
  const response = await fetch(`${BACKEND_URL}/api/download`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  
  if (!response.ok) {
    const err = await response.text();
    throw new Error(err);
  }
  
  return response.json();
}

/**
 * Kiểm tra backend có đang chạy không
 */
async function checkBackendStatus() {
  try {
    const r = await fetch(`${BACKEND_URL}/api/status`, { signal: AbortSignal.timeout(2000) });
    return r.ok;
  } catch {
    return false;
  }
}

// Kiểm tra backend khi extension khởi động
checkBackendStatus().then(ok => {
  if (!ok) {
    console.warn('[UniversalDownloader] Backend không chạy tại', BACKEND_URL);
  } else {
    console.log('[UniversalDownloader] Backend OK');
  }
});
