package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"universal-downloader/internal/downloader"
	"universal-downloader/internal/queue"
	"universal-downloader/internal/storage"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// Server là HTTP server để extension giao tiếp
type Server struct {
	queue   *queue.Manager
	db      *storage.Database
	ctx     context.Context
	hub     *wsHub
	engine  *gin.Engine
}

// wsHub quản lý WebSocket connections
type wsHub struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan []byte
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	mu         sync.Mutex
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Cho phép extension kết nối
	},
}

// NewServer tạo HTTP server mới
func NewServer(q *queue.Manager, db *storage.Database, ctx context.Context) *Server {
	gin.SetMode(gin.ReleaseMode)

	hub := &wsHub{
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan []byte, 100),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
	}
	go hub.run()

	s := &Server{
		queue:  q,
		db:     db,
		ctx:    ctx,
		hub:    hub,
		engine: gin.New(),
	}

	s.setupRoutes()
	return s
}

// Run khởi động server
func (s *Server) Run(addr string) error {
	return s.engine.Run(addr)
}

// BroadcastProgress gửi progress update tới tất cả WS clients
func (s *Server) BroadcastProgress(update queue.ProgressUpdate) {
	data, _ := json.Marshal(map[string]interface{}{
		"type": "progress",
		"data": update,
	})
	select {
	case s.hub.broadcast <- data:
	default:
	}
}

// setupRoutes đăng ký API routes
func (s *Server) setupRoutes() {
	r := s.engine

	// CORS — cho phép extension truy cập
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"*"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	api := r.Group("/api")

	// Status check
	api.GET("/status", s.handleStatus)

	// Download management
	api.POST("/download", s.handleAddDownload)
	api.GET("/queue", s.handleGetQueue)
	api.DELETE("/queue/:id", s.handleCancelDownload)
	api.POST("/queue/:id/retry", s.handleRetryDownload)
	api.DELETE("/queue/:id/remove", s.handleRemoveDownload)

	// History
	api.GET("/history", s.handleGetHistory)
	api.DELETE("/history", s.handleClearHistory)

	// Media info (lấy thông tin trước khi tải)
	api.POST("/info", s.handleGetInfo)

	// Settings
	api.GET("/settings", s.handleGetSettings)
	api.POST("/settings", s.handleSaveSettings)

	// Open window
	api.POST("/open-window", s.handleOpenWindow)

	// WebSocket cho real-time updates
	r.GET("/ws", s.handleWebSocket)
}

// ===== HANDLERS =====

func (s *Server) handleStatus(c *gin.Context) {
	c.JSON(200, gin.H{
		"status":  "ok",
		"version": "1.0.0",
		"queue":   len(s.queue.GetAll()),
	})
}

func (s *Server) handleAddDownload(c *gin.Context) {
	var req downloader.DownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	item, err := s.queue.Add(req)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"ok":   true,
		"id":   item.ID,
		"item": item,
	})
}

func (s *Server) handleGetQueue(c *gin.Context) {
	items := s.queue.GetAll()
	c.JSON(200, gin.H{"items": items})
}

func (s *Server) handleCancelDownload(c *gin.Context) {
	id := c.Param("id")
	if err := s.queue.Cancel(id); err != nil {
		c.JSON(404, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) handleRetryDownload(c *gin.Context) {
	id := c.Param("id")
	if err := s.queue.Retry(id); err != nil {
		c.JSON(404, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) handleRemoveDownload(c *gin.Context) {
	id := c.Param("id")
	if err := s.queue.Remove(id); err != nil {
		c.JSON(404, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) handleGetHistory(c *gin.Context) {
	records, err := s.db.GetRecentDownloads(200)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"records": records})
}

func (s *Server) handleClearHistory(c *gin.Context) {
	if err := s.db.ClearHistory(); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) handleGetInfo(c *gin.Context) {
	var body struct {
		URL string `json:"url"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// Lấy custom paths từ settings
	settings, _ := s.db.GetSettings()
	var ytdlpPath, ffmpegPath string
	if settings != nil {
		ytdlpPath = settings.YtdlpPath
		ffmpegPath = settings.FfmpegPath
	}

	// Xác định tools directory từ exe path
	exe, _ := os.Executable()
	toolsDir := filepath.Dir(exe)

	// Tạo engine mới
	engine := downloader.NewEngine(toolsDir)
	engine.SetPaths(ytdlpPath, ffmpegPath)

	info, err := engine.GetInfo(body.URL)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, info)
}

func (s *Server) handleGetSettings(c *gin.Context) {
	settings, err := s.db.GetSettings()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, settings)
}

func (s *Server) handleSaveSettings(c *gin.Context) {
	var settings storage.Settings
	if err := c.ShouldBindJSON(&settings); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := s.db.SaveSettings(settings); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) handleOpenWindow(c *gin.Context) {
	// Wails sẽ handle việc show window
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) handleWebSocket(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("WS upgrade error: %v", err)
		return
	}

	s.hub.register <- conn

	// Keep connection alive
	go func() {
		defer func() {
			s.hub.unregister <- conn
			conn.Close()
		}()

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}()
}

// ===== WS HUB =====

func (h *wsHub) run() {
	for {
		select {
		case conn := <-h.register:
			h.mu.Lock()
			h.clients[conn] = true
			h.mu.Unlock()

		case conn := <-h.unregister:
			h.mu.Lock()
			delete(h.clients, conn)
			h.mu.Unlock()
			conn.Close()

		case msg := <-h.broadcast:
			h.mu.Lock()
			for conn := range h.clients {
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					delete(h.clients, conn)
					conn.Close()
				}
			}
			h.mu.Unlock()
		}
	}
}
