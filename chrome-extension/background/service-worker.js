/**
 * UniversalDownloader — Background Service Worker (v3)
 * Detect media + extract quality/thumbnail
 */

// YouTube itag -> quality label
const YTDLP_ITAG_QUALITY = {
  '22':  { q: '720p',  label: '720p MP4 (video+audio)', type: 'video' },
  '18':  { q: '360p',  label: '360p MP4 (video+audio)', type: 'video' },
  '137': { q: '1080p', label: '1080p MP4 (video only)', type: 'video' },
  '248': { q: '1080p', label: '1080p WebM (video only)', type: 'video' },
  '136': { q: '720p',  label: '720p MP4 (video only)',  type: 'video' },
  '247': { q: '720p',  label: '720p WebM (video only)', type: 'video' },
  '135': { q: '480p',  label: '480p MP4 (video only)',  type: 'video' },
  '244': { q: '480p',  label: '480p WebM (video only)', type: 'video' },
  '134': { q: '360p',  label: '360p MP4 (video only)',  type: 'video' },
  '243': { q: '360p',  label: '360p WebM (video only)', type: 'video' },
  '133': { q: '240p',  label: '240p MP4 (video only)',  type: 'video' },
  '242': { q: '240p',  label: '240p WebM (video only)', type: 'video' },
  '160': { q: '144p',  label: '144p MP4 (video only)',  type: 'video' },
  '278': { q: '144p',  label: '144p WebM (video only)', type: 'video' },
  '140': { q: '128k',  label: 'M4A Audio 128kbps',      type: 'audio' },
  '251': { q: '160k',  label: 'WebM Audio 160kbps',     type: 'audio' },
  '139': { q: '48k',   label: 'M4A Audio 48kbps',       type: 'audio' },
  '171': { q: '128k',  label: 'WebM Audio 128kbps',     type: 'audio' },
};

function parseYouTubeStream(url) {
  try {
    const u = new URL(url);
    if (!u.hostname.includes('googlevideo.com')) return null;
    const itag = u.searchParams.get('itag');
    const info = itag ? YTDLP_ITAG_QUALITY[itag] : null;
    return {
      itag,
      quality: info?.q || u.searchParams.get('quality') || '',
      label:   info?.label || (itag ? `itag=${itag}` : ''),
      mediaType: info?.type || 'video',
    };
  } catch { return null; }
}

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
      pageThumbnail: '',
      ytdlpChecked: false,
    });
  }
  return tabMedia.get(tabId);
}

function addMediaToTab(tabId, item) {
  const data = getTabData(tabId);

  // Lọc HLS segments
  if (item.type === 'hls-segment') return;

  // YouTube stream: dedup theo itag (chất lượng) thay vì URL đầy đủ
  // (YouTube gửi nhiều requests cho cùng 1 quality để buffer)
  if (item.itag) {
    if (data.mediaList.some(m => m.itag === item.itag)) return;
  } else {
    // Dedup theo URL
    if (data.mediaList.some(m => m.url === item.url)) return;
  }

  // Nếu item không có thumbnail, dùng thumbnail của tab
  const thumbnail = item.thumbnail || data.pageThumbnail || '';
  const title = item.title || data.pageTitle || '';

  data.mediaList.push({
    id: Date.now() + Math.random(),
    timestamp: Date.now(),
    ...item,
    thumbnail,
    title,
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
      // Thử parse YouTube stream info
      const ytStream = parseYouTubeStream(url);
      const tabData = getTabData(tabId);

      addMediaToTab(tabId, {
        url,
        type: ytStream?.mediaType || classifyMedia(url, contentType),
        contentType,
        size: contentLength,
        source: 'network',
        quality: ytStream?.quality || extractQualityHint(url),
        qualityLabel: ytStream?.label || '',
        itag: ytStream?.itag || null,
        title: tabData.pageTitle || '',
        thumbnail: tabData.pageThumbnail || '',
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
      chrome.tabs.query({ active: true, currentWindow: true }, (tabs) => {
        const activeTabId = tabs[0]?.id || tabId;
        const data = getTabData(activeTabId);
        sendResponse({
          mediaList: data.mediaList,
          pageTitle: data.pageTitle,
          pageUrl: data.pageUrl,
          pageThumbnail: data.pageThumbnail || '',
          ytdlpChecked: data.ytdlpChecked,
        });
      });
      return true;
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
        if (message.thumbnail) data.pageThumbnail = message.thumbnail;
        // Cập nhật thumbnail cho tất cả items hiện có chưa có thumbnail
        for (const m of data.mediaList) {
          if (!m.thumbnail && message.thumbnail) m.thumbnail = message.thumbnail;
          if (!m.title && message.title) m.title = message.title;
        }
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
