/**
 * UniversalDownloader — Media Hunter v3
 * - Gửi thumbnail của trang (YouTube: dùng video ID)
 * - Gửi title thực từ DOM
 * - Detect quality từ <video> element
 */

(function() {
  'use strict';
  if (window.__universalDLInjected) return;
  window.__universalDLInjected = true;

  // ===== PAGE INFO + THUMBNAIL =====

  function getYouTubeVideoId(url) {
    try {
      const u = new URL(url);
      return u.searchParams.get('v') || u.pathname.slice(1); // youtu.be/ID
    } catch { return null; }
  }

  function getPageThumbnail() {
    // YouTube: dùng video ID để lấy thumbnail chất lượng cao
    const ytId = getYouTubeVideoId(location.href);
    if (ytId && ytId.length > 5) {
      return `https://img.youtube.com/vi/${ytId}/hqdefault.jpg`;
    }
    // OG image
    const og = document.querySelector('meta[property="og:image"]')?.content;
    if (og) return og;
    // Twitter card
    const tc = document.querySelector('meta[name="twitter:image"]')?.content;
    if (tc) return tc;
    // Poster của video đầu tiên
    const poster = document.querySelector('video[poster]')?.poster;
    if (poster) return poster;
    return '';
  }

  function getCleanTitle() {
    // YouTube: lấy từ h1.ytd-watch-metadata hoặc title thực
    const ytTitle =
      document.querySelector('h1.ytd-watch-metadata yt-formatted-string')?.textContent ||
      document.querySelector('h1.title')?.textContent ||
      document.querySelector('#title h1')?.textContent ||
      document.querySelector('meta[property="og:title"]')?.content ||
      document.querySelector('meta[name="twitter:title"]')?.content;
    if (ytTitle && ytTitle.trim().length > 0) return ytTitle.trim();
    return document.title.replace(/ - YouTube$/, '').replace(/ \| .+$/, '').trim();
  }

  function sendPageInfo() {
    chrome.runtime.sendMessage({
      type: 'PAGE_INFO',
      title: getCleanTitle(),
      url: location.href,
      thumbnail: getPageThumbnail(),
    });
  }

  // Gửi ngay
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', sendPageInfo);
  } else {
    sendPageInfo();
  }

  // Theo dõi khi title thay đổi (SPA navigation)
  const titleEl = document.querySelector('title');
  if (titleEl) {
    new MutationObserver(() => {
      setTimeout(sendPageInfo, 500); // Đợi nội dung page cập nhật
    }).observe(titleEl, { childList: true, characterData: true, subtree: true });
  }

  // YouTube SPA: theo dõi navigation qua yt-navigate-finish event
  document.addEventListener('yt-navigate-finish', () => {
    setTimeout(sendPageInfo, 1000);
  });

  // ===== MONITOR VIDEO/AUDIO ELEMENTS =====

  function monitorMediaElement(el) {
    const report = () => {
      const src = el.src || el.currentSrc;
      if (!src || src.startsWith('data:') || src.startsWith('blob:')) return;

      let mediaType = el.tagName === 'AUDIO' ? 'audio' : 'video';
      if (src.includes('.m3u8')) mediaType = 'hls';
      if (src.includes('.mpd')) mediaType = 'dash';

      chrome.runtime.sendMessage({
        type: 'MEDIA_FOUND',
        url: src,
        mediaType,
        source: 'video-element',
        title: getCleanTitle(),
        thumbnail: getPageThumbnail(),
      }).catch(() => {});
    };

    if (el.src || el.currentSrc) report();
    el.addEventListener('loadstart', report);
    el.addEventListener('canplay', report);
  }

  function scanAll() {
    document.querySelectorAll('video, audio').forEach(monitorMediaElement);
  }

  if (document.body) {
    new MutationObserver((mutations) => {
      for (const m of mutations) {
        for (const node of m.addedNodes) {
          if (node.nodeType !== 1) continue;
          if (node.matches?.('video, audio')) monitorMediaElement(node);
          node.querySelectorAll?.('video, audio').forEach(monitorMediaElement);
        }
      }
    }).observe(document.body, { childList: true, subtree: true });
    scanAll();
  } else {
    document.addEventListener('DOMContentLoaded', () => {
      new MutationObserver((mutations) => {
        for (const m of mutations) {
          for (const node of m.addedNodes) {
            if (node.nodeType !== 1) continue;
            if (node.matches?.('video, audio')) monitorMediaElement(node);
            node.querySelectorAll?.('video, audio').forEach(monitorMediaElement);
          }
        }
      }).observe(document.body, { childList: true, subtree: true });
      scanAll();
    });
  }

  // ===== NHẬN TỪ PAGE CONTEXT (inject.js) =====
  window.addEventListener('__universalDL_media', (event) => {
    const data = event.detail;
    if (!data?.url) return;
    chrome.runtime.sendMessage({
      type: 'MEDIA_FOUND',
      url: data.url,
      mediaType: data.mediaType || 'video',
      source: data.source || 'page-hook',
      title: data.title || getCleanTitle(),
      thumbnail: data.thumbnail || getPageThumbnail(),
      contentType: data.contentType || '',
      size: data.size || 0,
      blobData: data.blobData || null,
    }).catch(() => {});
  });

  // ===== INJECT SCRIPT VÀO PAGE CONTEXT =====
  const s = document.createElement('script');
  s.src = chrome.runtime.getURL('content/inject.js');
  s.onload = () => s.remove();
  (document.head || document.documentElement).appendChild(s);

})();
