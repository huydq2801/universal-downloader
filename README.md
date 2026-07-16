# 🚀 UniversalDownloader

> Tải mọi video và media từ bất kỳ trang web nào — kể cả trang có chống tải.

## Kiến trúc

```
Chrome Extension  ←→  Go Backend (Wails)  ←→  yt-dlp / ffmpeg
```

- **Chrome Extension**: Phát hiện media trên trang web (video, audio, stream)
- **Go Desktop App**: Quản lý hàng đợi tải, xử lý stream, lưu lịch sử
- **yt-dlp + ffmpeg**: Engine tải và xử lý video mạnh nhất

## Tính năng

- ✅ Phát hiện video/audio/stream tự động khi duyệt web
- ✅ Hỗ trợ HLS (`.m3u8`), DASH (`.mpd`), MP4, WebM, blob URL
- ✅ Vượt qua bảo vệ chống tải bằng nhiều chiến lược
- ✅ Chọn chất lượng video trước khi tải
- ✅ Hàng đợi tải với progress real-time
- ✅ Lịch sử tải

## Cài đặt

### Yêu cầu

- Go 1.21+
- Node.js 18+
- Wails v2: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- yt-dlp: [Tải tại đây](https://github.com/yt-dlp/yt-dlp/releases)
- ffmpeg: [Tải tại đây](https://ffmpeg.org/download.html)

### Build

```bash
cd desktop-app
wails build
```

### Dev mode

```bash
cd desktop-app
wails dev
```

### Cài Extension

1. Mở Chrome → `chrome://extensions/`
2. Bật **Developer mode**
3. Click **Load unpacked** → chọn thư mục `chrome-extension/`

## Cấu trúc

```
universal-downloader/
├── chrome-extension/     # Chrome Extension (Manifest V3)
│   ├── background/       # Service Worker — bắt network requests
│   ├── content/          # Content Scripts — hook fetch/XHR/blob
│   └── popup/            # Popup UI — chọn video để tải
└── desktop-app/          # Wails Desktop App (Go)
    ├── internal/
    │   ├── server/       # HTTP API server (Gin)
    │   ├── downloader/   # yt-dlp/ffmpeg wrapper
    │   ├── queue/        # Download queue manager
    │   └── storage/      # SQLite database
    └── frontend/         # Dashboard UI (HTML/CSS/JS)
```

## License

MIT — Chỉ dùng cho nội dung bạn có quyền tải.
