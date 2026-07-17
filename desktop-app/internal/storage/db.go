package storage

import (
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Database wraps gorm DB
type Database struct {
	db *gorm.DB
}

// DownloadRecord lưu lịch sử download
type DownloadRecord struct {
	ID          string    `gorm:"primaryKey" json:"id"`
	URL         string    `gorm:"not null" json:"url"`
	PageURL     string    `json:"pageUrl"`
	Title       string    `json:"title"`
	Type        string    `json:"type"` // video, hls, dash, audio, blob
	Status      string    `json:"status"` // pending, downloading, completed, failed, cancelled
	Quality     string    `json:"quality"`
	Format      string    `json:"format"`
	SavePath    string    `json:"savePath"`
	FileSize    int64     `json:"fileSize"`
	Progress    float64   `json:"progress"`
	Speed       string    `json:"speed"`
	ETA         string    `json:"eta"`
	Error       string    `json:"error"`
	Thumbnail   string    `json:"thumbnail"`
	Duration    string    `json:"duration"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	CompletedAt *time.Time `json:"completedAt"`
}

// Settings lưu cấu hình app
type Settings struct {
	ID              uint   `gorm:"primaryKey" json:"-"`
	SaveDirectory   string `json:"saveDirectory"`
	DefaultQuality  string `json:"defaultQuality"`
	DefaultFormat   string `json:"defaultFormat"`
	MaxConcurrent   int    `json:"maxConcurrent"`
	DownloadThreads int    `json:"downloadThreads"`
	YtdlpPath       string `json:"ytdlpPath"`
	FfmpegPath      string `json:"ffmpegPath"`
	AutoOpenFolder  bool   `json:"autoOpenFolder"`
	Theme           string `json:"theme"`
	GeminiAPIKey    string `json:"geminiAPIKey"`
	GeminiModel     string `json:"geminiModel"`
}

// NewDatabase khởi tạo SQLite database
func NewDatabase(path string) (*Database, error) {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}

	// Auto-migrate schemas
	if err := db.AutoMigrate(&DownloadRecord{}, &Settings{}); err != nil {
		return nil, err
	}

	return &Database{db: db}, nil
}

// Close đóng database
func (d *Database) Close() error {
	sqlDB, err := d.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// ===== Download Records =====

// CreateRecord tạo record mới
func (d *Database) CreateRecord(record *DownloadRecord) error {
	return d.db.Create(record).Error
}

// UpdateRecord cập nhật record
func (d *Database) UpdateRecord(record *DownloadRecord) error {
	return d.db.Save(record).Error
}

// UpdateStatus cập nhật status và progress
func (d *Database) UpdateStatus(id, status string, progress float64, speed, eta, errMsg string) error {
	updates := map[string]interface{}{
		"status":   status,
		"progress": progress,
		"speed":    speed,
		"eta":      eta,
		"error":    errMsg,
	}
	if status == "completed" {
		now := time.Now()
		updates["completed_at"] = &now
	}
	return d.db.Model(&DownloadRecord{}).Where("id = ?", id).Updates(updates).Error
}

// GetRecentDownloads lấy danh sách download gần đây
func (d *Database) GetRecentDownloads(limit int) ([]DownloadRecord, error) {
	var records []DownloadRecord
	err := d.db.Order("created_at DESC").Limit(limit).Find(&records).Error
	return records, err
}

// GetRecord lấy record theo ID
func (d *Database) GetRecord(id string) (*DownloadRecord, error) {
	var record DownloadRecord
	err := d.db.First(&record, "id = ?", id).Error
	return &record, err
}

// DeleteRecord xóa record
func (d *Database) DeleteRecord(id string) error {
	return d.db.Delete(&DownloadRecord{}, "id = ?", id).Error
}

// ClearHistory xóa toàn bộ lịch sử
func (d *Database) ClearHistory() error {
	return d.db.Where("status IN ?", []string{"completed", "failed", "cancelled"}).Delete(&DownloadRecord{}).Error
}

// ===== Settings =====

// GetSettings lấy settings (tạo mới nếu chưa có)
func (d *Database) GetSettings() (*Settings, error) {
	var settings Settings
	result := d.db.First(&settings)
	if result.Error != nil {
		// Tạo settings mặc định
		homeDir := getDefaultDownloadDir()
		settings = Settings{
			SaveDirectory:   homeDir,
			DefaultQuality:  "best",
			DefaultFormat:   "mp4",
			MaxConcurrent:   3,
			DownloadThreads: 8,
			YtdlpPath:       "yt-dlp",
			FfmpegPath:      "ffmpeg",
			AutoOpenFolder:  false,
			Theme:           "dark",
		}
		if err := d.db.Create(&settings).Error; err != nil {
			return nil, err
		}
	}
	return &settings, nil
}

// SaveSettings lưu settings
func (d *Database) SaveSettings(settings Settings) error {
	settings.ID = 1 // Luôn dùng ID=1
	return d.db.Save(&settings).Error
}
