# 🚀 UniversalDownloader

> A high-performance, general-purpose media and video downloader designed to fetch streams from any website, bypassing standard network restrictions, firewalls, and advanced stream protection.

## 🛠️ System Architecture

```
Chrome Extension  ──(Network Sniffer)──→  Go Backend (Wails)  ──(Parallel Downloader)──→  Final MP4 File
                                                  │
                                                  └──→  yt-dlp / FFmpeg (Standard Platform Fallback)
```

- **Chrome Extension**: Intercepts media-related network requests (XHR, fetch, m3u8, MPD, blob URLs) and forwards them directly to the desktop app.
- **Go Desktop App**: Manages the download queue, resolves domain names bypassing local censorship/blocks, downloads HLS segments concurrently, decapsulates masked headers, and packages media streams.
- **yt-dlp + FFmpeg Integration**: Acts as a fallback engine to download and merge streams from mainstream platforms (YouTube, TikTok, etc.).

---

## ✨ Core Features

1. **Go Native HLS Downloader (Multi-threaded & Parallel):**
   - **Parallel Segment Fetching (8+ Threads)**: Speeds up downloading by pulling up to 8 HLS chunks simultaneously, optimizing network bandwidth utilization.
   - **PNG Decapsulation (Masked Streams)**: Detects stream segments disguised as PNG image files (a common technique to bypass standard downloading utilities). Automatically strips the mock image headers on-the-fly to reconstruct clean video payloads.
   - **Rate Limit & Referer Optimization**: Dynamically adjusts headers and strips `Referer` fields when resolving streams hosted on rate-limited CDNs (e.g., Google Drive) to completely eliminate `HTTP 429 Too Many Requests` issues.

2. **Censorship & Firewall Bypass (Direct DNS Resolver):**
   - Built-in custom dialer bypassing OS-level DNS blockages using secure DNS resolvers (Google `8.8.8.8` & Cloudflare `1.1.1.1`).
   - Restores connectivity and extracts streams from censored domains without requiring system-wide VPNs or proxy setups.

3. **Advanced Playlist Quality Fallback:**
   - **Bandwidth-based Quality Sorting**: Automatically parses Master HLS playlists and sorts all available stream variants by quality.
   - **Sequential Fallback Loop**: Attempts to download variants starting from the highest resolution (1080p, 720p, etc.). If a particular CDN variant returns a 404 (due to unrendered streams on the server), the downloader automatically falls back to the next best variant instead of failing.
   - **Query Parameter Preservation**: Automatically propagates CORS signatures and CDN access tokens (`?token=...`, `?v=...`) to all child playlists and segment URLs.

---

## 💻 Installation & Usage

### Prerequisites
- **Go**: Version 1.21 or higher
- **Node.js**: Version 18 or higher
- **Wails CLI**: Install using Go:
  ```bash
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  ```
- **yt-dlp**: Handled and configured automatically.
- **FFmpeg**: Handled and configured automatically.

### Compile and Build
1. Navigate to the desktop application directory:
   ```bash
   cd desktop-app
   ```
2. Build the application:
   ```bash
   wails build -o "UniversalDownloader.exe"
   ```

### Development Mode
```bash
cd desktop-app
wails dev
```

### Install Chrome Extension
1. Open Google Chrome and go to `chrome://extensions/`.
2. Enable **Developer mode** in the top-right corner.
3. Click **Load unpacked** and select the `chrome-extension/` directory in the root of the project.

---

## 📂 Project Structure

```
universal-downloader/
├── chrome-extension/     # Chrome Extension (Manifest V3) for network sniffing
│   ├── background/       # Service worker listening to network requests
│   ├── content/          # Content scripts hooking blob/fetch APIs
│   └── popup/            # Extension UI for manually sending links to desktop app
└── desktop-app/          # Wails Desktop App (Go + JS Frontend)
    ├── app.go            # Wails binding interface & bridge controller
    ├── internal/
    │   ├── server/       # Local API server receiving payloads from extension
    │   ├── downloader/   # Media download engine: yt-dlp, FFmpeg, and Custom HLS
    │   │   ├── engine.go       # Downloader routing controller
    │   │   └── custom_hls.go   # Parallel segment downloader & decapsulator
    │   ├── queue/        # Thread-safe download queue manager
    │   └── storage/      # Local SQLite database (GORM) for download history
    └── frontend/         # Sleek, modern responsive UI dashboard
```

---

## 📝 License & Disclaimer
This project is distributed under the MIT License. Use this software only for downloading media assets that you own or have legal authorization to download. The authors are not responsible for any misuse of the application.
