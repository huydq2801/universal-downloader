/**
 * UniversalDownloader — Media Hunter Content Script
 * 
 * Chạy trong context của trang web, hook vào:
 * 1. fetch() API
 * 2. XMLHttpRequest
 * 3. <video> và <audio> elements
 * 4. URL.createObjectURL (blob URLs)
 * 
 * Chú ý: Script này chạy trong isolated world của extension.
 * Để hook vào page's JS context, cần inject qua inject.js (xem stream-interceptor.js)
 */

(function() {
  'use strict';
  
  // Tránh inject nhiều lần
  if (window.__universalDLInjected) return;
  window.__universalDLInjected = true;
  
  // Gửi page info lên background
  chrome.runtime.sendMessage({
    type: 'PAGE_INFO',
    title: document.title,
    url: location.href,
  });
  
  // Cập nhật title khi thay đổi
  const titleObserver = new MutationObserver(() => {
    chrome.runtime.sendMessage({
      type: 'PAGE_INFO',
      title: document.title,
      url: location.href,
    });
  });
  titleObserver.observe(document.querySelector('title') || document.head, {
    childList: true,
    subtree: true,
    characterData: true,
  });

  // ===== PHÁT HIỆN VIDEO/AUDIO ELEMENTS =====
  
  /**
   * Theo dõi và báo cáo video/audio elements
   */
  function monitorMediaElement(el) {
    const reportSource = () => {
      const src = el.src || el.currentSrc;
      if (!src || src.startsWith('data:')) return;
      
      let mediaType = 'video';
      if (el.tagName === 'AUDIO') mediaType = 'audio';
      if (src.includes('.m3u8')) mediaType = 'hls';
      if (src.includes('.mpd')) mediaType = 'dash';
      
      // Lấy thumbnail cho video
      let thumbnail = '';
      try {
        const poster = el.getAttribute('poster');
        if (poster) thumbnail = new URL(poster, location.href).href;
      } catch {}
      
      // Lấy title từ context
      const title = el.title || el.getAttribute('aria-label') || 
                    el.closest('[aria-label]')?.getAttribute('aria-label') ||
                    document.title;
      
      chrome.runtime.sendMessage({
        type: 'MEDIA_FOUND',
        url: src,
        mediaType,
        source: 'video-element',
        title,
        thumbnail,
        size: 0,
      }).catch(() => {});
    };
    
    // Báo cáo ngay nếu đã có src
    if (el.src || el.currentSrc) reportSource();
    
    // Theo dõi thay đổi src
    el.addEventListener('loadstart', reportSource);
    el.addEventListener('canplay', reportSource);
  }
  
  /**
   * Scan DOM tìm video/audio elements hiện tại
   */
  function scanExistingElements() {
    document.querySelectorAll('video, audio').forEach(monitorMediaElement);
  }
  
  /**
   * Theo dõi DOM mutations để bắt video/audio được thêm sau
   */
  const mediaObserver = new MutationObserver((mutations) => {
    for (const mutation of mutations) {
      for (const node of mutation.addedNodes) {
        if (node.nodeType !== Node.ELEMENT_NODE) continue;
        
        if (node.matches?.('video, audio')) {
          monitorMediaElement(node);
        }
        
        node.querySelectorAll?.('video, audio').forEach(monitorMediaElement);
      }
    }
  });
  
  // Bắt đầu observe ngay khi DOM available
  if (document.body) {
    mediaObserver.observe(document.body, { childList: true, subtree: true });
    scanExistingElements();
  } else {
    document.addEventListener('DOMContentLoaded', () => {
      mediaObserver.observe(document.body, { childList: true, subtree: true });
      scanExistingElements();
    });
  }
  
  // ===== NHẬN TIN NHẮN TỪ PAGE CONTEXT =====
  
  // inject.js (chạy trong page context) sẽ gửi events qua CustomEvent
  window.addEventListener('__universalDL_media', (event) => {
    const data = event.detail;
    if (!data || !data.url) return;
    
    chrome.runtime.sendMessage({
      type: 'MEDIA_FOUND',
      url: data.url,
      mediaType: data.mediaType || 'video',
      source: data.source || 'page-hook',
      title: data.title || document.title,
      contentType: data.contentType || '',
      size: data.size || 0,
      blobData: data.blobData || null,
    }).catch(() => {});
  });
  
  // ===== INJECT SCRIPT VÀO PAGE CONTEXT =====
  
  // inject.js cần chạy trong page context để hook fetch/XHR/blob
  const script = document.createElement('script');
  script.src = chrome.runtime.getURL('content/inject.js');
  script.onload = () => script.remove();
  (document.head || document.documentElement).appendChild(script);
  
})();
