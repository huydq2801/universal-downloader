# 🚀 UniversalDownloader

> Trình tải đa năng mọi video và media từ bất kỳ trang web nào — kể cả các trang web chặn IP, chặn DNS và chống tải nâng cao.

## 🛠️ Kiến trúc hệ thống

```
Chrome Extension  ←──(Phát hiện URL)──→  Go Backend (Wails)  ←──(Song song / Giải mã)──→  File MP4 hoàn chỉnh
                                                 │
                                                 └──→  yt-dlp / ffmpeg (Dành cho trang phổ thông)
```

- **Chrome Extension**: Bắt các network request (XHR, fetch, m3u8, blob URL) và gửi dữ liệu về ứng dụng desktop.
- **Go Desktop App**: Quản lý hàng đợi, tự động giải quyết chặn DNS/IP, tải đa luồng song song các phân mảnh video, giải mã ngụy trang tệp ảnh.
- **yt-dlp + ffmpeg**: Sử dụng làm công cụ bổ trợ tải và ghép các luồng video từ các nền tảng phổ thông (YouTube, TikTok...).

---

## ✨ Tính năng nổi bật mới

1. **Bộ tải HLS Go Native (Đa luồng song song & Giải mã PNG):**
   - **Tải song song 8 phân mảnh (8 Workers):** Đẩy tốc độ tải luồng HLS nhanh gấp 10 lần so với tải tuần tự thông thường, tối ưu hóa tối đa băng thông mạng.
   - **Tự động hóa giải mã PNG (PNG Decapsulation):** Tự động phát hiện các phân mảnh video bị ngụy trang thành file ảnh PNG (thường gặp trên các nền tảng chia sẻ video lậu). Ứng dụng tự động bóc tách và loại bỏ 941 byte header ảnh giả lập trên luồng bay (on-the-fly) để trả về tệp video chuẩn.
   - **Tránh bị chặn HTTP 429:** Tự động tối ưu hóa và loại bỏ `Referer` khi yêu cầu các phân mảnh trên Google Drive, khắc phục hoàn toàn lỗi `HTTP 429 Too Many Requests`.

2. **Vượt tường lửa DNS (Direct DNS Resolver):**
   - Tích hợp bộ phân giải tên miền (DNS Resolver) trực tiếp bằng Go qua DNS công cộng Cloudflare (`1.1.1.1`) và Google (`8.8.8.8`).
   - Bỏ qua hoàn toàn cấu hình chặn DNS của nhà mạng Việt Nam hoặc file `hosts` của hệ thống để phân tích mà không cần bật VPN/1.1.1.1 trên toàn máy.

3. **Thuật toán bóc tách Iframe & Token nâng cao:**
   - Tích hợp pipeline 3 giai đoạn tự động giải mã liên kết, đảo chuỗi mã hóa token `OLID` để trích xuất trực tiếp luồng HLS chất lượng cao nhất từ các trình phát của bên thứ ba.

---

## 💻 Cài đặt & Sử dụng

### Yêu cầu hệ thống
- **Go**: Phiên bản 1.21 trở lên
- **Node.js**: Phiên bản 18 trở lên
- **Wails v2**: Cài đặt thông qua lệnh:
  ```bash
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  ```
- **yt-dlp**: Đã được ứng dụng tự động kiểm tra và cấu hình.
- **ffmpeg**: Đã được ứng dụng tự động kiểm tra và cấu hình.

### Biên dịch ứng dụng (Build)
1. Di chuyển vào thư mục ứng dụng desktop:
   ```bash
   cd desktop-app
   ```
2. Thực hiện build ứng dụng wails:
   ```bash
   wails build -o "UniversalDownloader.exe"
   ```

### Chạy chế độ nhà phát triển (Dev Mode)
```bash
cd desktop-app
wails dev
```

### Cài đặt Chrome Extension
1. Truy cập vào quản lý extension của Chrome: `chrome://extensions/`
2. Bật góc phải màn hình chế độ **Developer mode**.
3. Chọn **Load unpacked** và trỏ tới thư mục `chrome-extension/` ở trong mã nguồn dự án.

---

## 📂 Cấu trúc dự án

```
universal-downloader/
├── chrome-extension/     # Chrome Extension (Manifest V3) để bắt link tự động
│   ├── background/       # Service Worker lắng nghe network request
│   ├── content/          # Content Scripts hook các API blob/fetch/XHR
│   └── popup/            # Giao diện tiện ích nhỏ để gửi link về ứng dụng
└── desktop-app/          # Wails Desktop App (Go + Svelte/JS Frontend)
    ├── app.go            # Cầu nối (Binding) Wails API, giải mã & vượt chặn DNS
    ├── internal/
    │   ├── server/       # API Server nội bộ đón nhận yêu cầu từ Extension
    │   ├── downloader/   # Engine tải video: yt-dlp, ffmpeg và Custom HLS Downloader
    │   │   ├── engine.go       # Bộ định tuyến luồng tải video
    │   │   └── custom_hls.go   # Bộ tải đa luồng song song, giải mã ngụy trang PNG
    │   ├── queue/        # Quản lý hàng đợi tải xuống
    │   └── storage/      # Cơ sở dữ liệu SQLite cục bộ (lưu lịch sử tải)
    └── frontend/         # Giao diện người dùng Dashboard UI sang trọng, hiện đại
```

---

## 📝 Bản quyền & Tuyên bố miễn trừ
Dự án được phân phối theo giấy phép MIT. Chỉ sử dụng phần mềm này để tải các tài nguyên mà bạn có quyền sở hữu hoặc được phép tải hợp pháp. Tác giả không chịu bất kỳ trách nhiệm nào đối với hành vi sử dụng sai mục đích.
