/**
 * UniversalDownloader — Stream Interceptor (Page Context)
 * 
 * File này được inject vào PAGE CONTEXT (không phải extension context)
 * để hook vào các API JavaScript của trang:
 * 
 * 1. fetch() — bắt các XHR/fetch calls tới media URLs
 * 2. XMLHttpRequest — tương tự fetch
 * 3. URL.createObjectURL — bắt blob URLs
 * 4. MediaSource.addSourceBuffer — bắt MSE streams
 * 5. SourceBuffer.appendBuffer — bắt data chunks của stream
 * 
 * Giao tiếp với content script qua CustomEvent vì khác context.
 */

(function(window) {
  'use strict';
  
  if (window.__universalDLPageInjected) return;
  window.__universalDLPageInjected = true;
  
  /**
   * Gửi media đã tìm thấy về content script
   */
  function reportMedia(info) {
    window.dispatchEvent(new CustomEvent('__universalDL_media', { detail: info }));
  }
  
  /**
   * Kiểm tra URL có phải media
   */
  function isMediaUrl(url) {
    if (!url || typeof url !== 'string') return false;
    const patterns = [
      /\.(mp4|webm|mkv|avi|mov|flv|m4v|3gp)/i,
      /\.m3u8/i,
      /\.mpd/i,
      /\.mp3|\.aac|\.ogg|\.wav|\.flac/i,
      /googlevideo\.com/i,
      /fbcdn\.net/i,
      /video\.twimg\.com/i,
      /manifest/i,
      /playlist/i,
      /stream/i,
    ];
    return patterns.some(p => p.test(url));
  }
  
  /**
   * Phân loại media type từ URL hoặc content-type
   */
  function getMediaType(url, contentType = '') {
    if (/\.m3u8/i.test(url) || /mpegURL/i.test(contentType)) return 'hls';
    if (/\.mpd/i.test(url) || /dash\+xml/i.test(contentType)) return 'dash';
    if (/\.(mp3|aac|ogg|wav|flac)/i.test(url) || contentType.startsWith('audio/')) return 'audio';
    return 'video';
  }
  
  // ===== HOOK fetch() =====
  
  const originalFetch = window.fetch;
  window.fetch = async function(...args) {
    const url = args[0] instanceof Request ? args[0].url : String(args[0]);
    
    const response = await originalFetch.apply(this, args);
    
    if (isMediaUrl(url)) {
      const contentType = response.headers.get('content-type') || '';
      const contentLength = parseInt(response.headers.get('content-length')) || 0;
      
      reportMedia({
        url,
        mediaType: getMediaType(url, contentType),
        contentType,
        size: contentLength,
        source: 'fetch-hook',
        title: document.title,
      });
    }
    
    return response;
  };
  
  // ===== HOOK XMLHttpRequest =====
  
  const OriginalXHR = window.XMLHttpRequest;
  window.XMLHttpRequest = function() {
    const xhr = new OriginalXHR();
    const originalOpen = xhr.open.bind(xhr);
    let capturedUrl = '';
    
    xhr.open = function(method, url, ...rest) {
      capturedUrl = String(url);
      return originalOpen(method, url, ...rest);
    };
    
    xhr.addEventListener('readystatechange', function() {
      if (xhr.readyState === 4 && isMediaUrl(capturedUrl)) {
        const contentType = xhr.getResponseHeader('content-type') || '';
        reportMedia({
          url: capturedUrl,
          mediaType: getMediaType(capturedUrl, contentType),
          contentType,
          size: 0,
          source: 'xhr-hook',
          title: document.title,
        });
      }
    });
    
    return xhr;
  };
  
  // Copy static properties
  Object.setPrototypeOf(window.XMLHttpRequest, OriginalXHR);
  Object.assign(window.XMLHttpRequest, OriginalXHR);
  
  // ===== HOOK URL.createObjectURL (Blob URLs) =====
  
  const originalCreateObjectURL = URL.createObjectURL.bind(URL);
  URL.createObjectURL = function(blob) {
    const blobUrl = originalCreateObjectURL(blob);
    
    if (blob instanceof Blob && blob.type && blob.type.startsWith('video/') || 
        blob instanceof Blob && blob.type && blob.type.startsWith('audio/')) {
      
      // Đọc một phần blob để xác nhận
      const reader = new FileReader();
      reader.onload = function(e) {
        const base64 = e.target.result;
        reportMedia({
          url: blobUrl,
          mediaType: blob.type.startsWith('audio/') ? 'audio' : 'video',
          contentType: blob.type,
          size: blob.size,
          source: 'blob-hook',
          title: document.title,
          // Gửi kèm base64 data để backend có thể tải (blob URLs không accessible ngoài page)
          blobData: base64,
        });
      };
      
      // Chỉ đọc blob nhỏ (< 50MB) để tránh memory issues
      if (blob.size < 50 * 1024 * 1024) {
        reader.readAsDataURL(blob);
      } else {
        reportMedia({
          url: blobUrl,
          mediaType: 'video',
          contentType: blob.type,
          size: blob.size,
          source: 'blob-hook',
          title: document.title,
          blobData: null, // Quá lớn, không capture
        });
      }
    }
    
    return blobUrl;
  };
  
  // ===== HOOK MediaSource (MSE - Media Source Extensions) =====
  
  // MSE là cách modern players (YouTube, Netflix...) stream video
  // Hook vào addSourceBuffer để biết MIME types được dùng
  // Hook vào appendBuffer để capture raw stream data
  
  if (window.MediaSource) {
    const originalAddSourceBuffer = MediaSource.prototype.addSourceBuffer;
    MediaSource.prototype.addSourceBuffer = function(mimeType) {
      const sourceBuffer = originalAddSourceBuffer.call(this, mimeType);
      const mediaSource = this;
      
      // Track MIME type
      if (!mediaSource.__udlMimeTypes) mediaSource.__udlMimeTypes = new Set();
      mediaSource.__udlMimeTypes.add(mimeType);
      
      return sourceBuffer;
    };
    
    // Hook MediaSource để theo dõi URL object được tạo
    const originalMediaSourceOpen = Object.getOwnPropertyDescriptor(MediaSource.prototype, 'readyState');
  }
  
  // ===== HOOK HTMLMediaElement.src setter =====
  
  // Bắt khi src được gán trực tiếp qua JS
  const mediaElements = ['HTMLVideoElement', 'HTMLAudioElement'];
  
  for (const elemType of mediaElements) {
    if (!window[elemType]) continue;
    
    const proto = window[elemType].prototype;
    const originalSrcDescriptor = Object.getOwnPropertyDescriptor(proto, 'src') ||
                                   Object.getOwnPropertyDescriptor(HTMLMediaElement.prototype, 'src');
    
    if (!originalSrcDescriptor) continue;
    
    Object.defineProperty(proto, 'src', {
      get() {
        return originalSrcDescriptor.get?.call(this) ?? '';
      },
      set(value) {
        if (value && isMediaUrl(String(value))) {
          reportMedia({
            url: String(value),
            mediaType: elemType === 'HTMLAudioElement' ? 'audio' : 'video',
            source: 'src-setter',
            title: document.title,
            size: 0,
          });
        }
        originalSrcDescriptor.set?.call(this, value);
      },
      configurable: true,
    });
  }
  
  // ===== HOOK document.createElement cho dynamic video/audio =====
  
  const originalCreateElement = document.createElement.bind(document);
  document.createElement = function(tagName, ...args) {
    const el = originalCreateElement(tagName, ...args);
    
    if (typeof tagName === 'string' && /^(video|audio)$/i.test(tagName)) {
      // Theo dõi src attribute changes
      const observer = new MutationObserver((mutations) => {
        for (const m of mutations) {
          if (m.attributeName === 'src') {
            const src = el.getAttribute('src');
            if (src && isMediaUrl(src)) {
              reportMedia({
                url: new URL(src, location.href).href,
                mediaType: tagName.toLowerCase() === 'audio' ? 'audio' : 'video',
                source: 'createelement-hook',
                title: document.title,
              });
            }
          }
        }
      });
      observer.observe(el, { attributes: true, attributeFilter: ['src'] });
    }
    
    return el;
  };
  
  console.log('[UniversalDownloader] Page hooks installed ✓');
  
})(window);
