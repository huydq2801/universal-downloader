# 🚀 UniversalDownloader

> A high-performance standalone desktop media and video downloader designed to fetch and save streams from any website, bypassing network restrictions, DNS/IP blocks, and custom stream encapsulation.

## 🛠️ System Architecture

```
User Input URL  ──→  Go Backend (Wails)  ──(Parallel Downloader)──→  Final MP4 File
                             │
                             └──→  yt-dlp / FFmpeg (Standard Platform Fallback)
```

- **Go Desktop App (Wails)**: Standalone desktop GUI that manages the download queue, resolves domain names using direct DNS, downloads HLS segments concurrently, decapsulates masked headers, and packages media streams.
- **yt-dlp + FFmpeg Integration**: Integrated backend engines to download and remux streams from mainstream video sharing platforms (YouTube, TikTok, etc.).

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

---

## 📂 Project Structure

```
universal-downloader/
└── desktop-app/          # Wails Desktop App (Go + JS Frontend)
    ├── app.go            # Wails binding interface & bridge controller
    ├── internal/
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
