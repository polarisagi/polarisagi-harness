package server

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// RateLimiter 基于 Token Bucket 实现单桶限流
type RateLimiter struct {
	mu     sync.Mutex
	tokens int
	last   time.Time
	rate   int
	max    int
}

func NewRateLimiter(rate, max int) *RateLimiter {
	return &RateLimiter{
		tokens: max,
		last:   time.Now(),
		rate:   rate,
		max:    max,
	}
}

func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.last).Seconds()
	rl.tokens += int(elapsed * float64(rl.rate))
	if rl.tokens > rl.max {
		rl.tokens = rl.max
	}
	rl.last = now

	if rl.tokens > 0 {
		rl.tokens--
		return true
	}
	return false
}

// RateLimitManager 按标识符（IP/Fingerprint）隔离限流桶
type RateLimitManager struct {
	mu       sync.RWMutex
	limiters map[string]*RateLimiter
	rate     int
	max      int
}

func NewRateLimitManager(rate, max int) *RateLimitManager {
	return &RateLimitManager{
		limiters: make(map[string]*RateLimiter),
		rate:     rate,
		max:      max,
	}
}

func (rm *RateLimitManager) Allow(key string) bool {
	rm.mu.RLock()
	limiter, exists := rm.limiters[key]
	rm.mu.RUnlock()

	if !exists {
		rm.mu.Lock()
		limiter, exists = rm.limiters[key]
		if !exists {
			limiter = NewRateLimiter(rm.rate, rm.max)
			rm.limiters[key] = limiter
		}
		rm.mu.Unlock()
	}

	return limiter.Allow()
}

// AuthManager 管理鉴权防爆破
type AuthManager struct {
	mu       sync.Mutex
	failures map[string]int
	lockedAt map[string]time.Time
}

func NewAuthManager() *AuthManager {
	return &AuthManager{
		failures: make(map[string]int),
		lockedAt: make(map[string]time.Time),
	}
}

func (am *AuthManager) IsLocked(ip string) bool {
	am.mu.Lock()
	defer am.mu.Unlock()

	lockedTime, exists := am.lockedAt[ip]
	if !exists {
		return false
	}
	// 5 分钟冷却
	if time.Since(lockedTime) > 5*time.Minute {
		delete(am.failures, ip)
		delete(am.lockedAt, ip)
		return false
	}
	return true
}

func (am *AuthManager) RecordFailure(ip string) {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.failures[ip]++
	// 连续 3 次失败即锁定
	if am.failures[ip] >= 3 {
		am.lockedAt[ip] = time.Now()
	}
}

func (am *AuthManager) RecordSuccess(ip string) {
	am.mu.Lock()
	defer am.mu.Unlock()

	delete(am.failures, ip)
	delete(am.lockedAt, ip)
}

func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	// r.RemoteAddr 通常包含端口
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// LoggingResponseWriter intercepts HTTP responses to capture the status code and body for logging.
type LoggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
	body       []byte
}

func NewLoggingResponseWriter(w http.ResponseWriter) *LoggingResponseWriter {
	return &LoggingResponseWriter{w, http.StatusOK, nil}
}

func (lrw *LoggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *LoggingResponseWriter) Write(b []byte) (int, error) {
	if lrw.statusCode >= 400 {
		lrw.body = append(lrw.body, b...)
	}
	return lrw.ResponseWriter.Write(b)
}

func (lrw *LoggingResponseWriter) Flush() {
	if flusher, ok := lrw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// withMiddleware 挂载所有基础网关级别的安全防护（Auth + Rate Limit + CORS + Logging）
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	// 按照 M13 规范，为每个 IP 分配一个单独的桶，限制默认并发 QPS
	limiter := NewRateLimitManager(20, 50)
	authManager := NewAuthManager()

	// API 密钥，如果在环境中设置了则进行验证
	expectedKey := os.Getenv("POLARIS_API_KEY")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lrw := NewLoggingResponseWriter(w)
		w = lrw

		clientIP := extractIP(r)

		isAPI := strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/healthz"
		defer func() {
			if isAPI {
				if lrw.statusCode >= 500 {
					slog.Error("http: request failed", "method", r.Method, "path", r.URL.Path, "ip", clientIP, "status", lrw.statusCode, "error", strings.TrimSpace(string(lrw.body)))
				} else if lrw.statusCode >= 400 {
					slog.Warn("http: bad request", "method", r.Method, "path", r.URL.Path, "ip", clientIP, "status", lrw.statusCode, "error", strings.TrimSpace(string(lrw.body)))
				}
			}
		}()

		// CORS
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-API-Key")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// 速率限制隔离
		if !limiter.Allow(clientIP) {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
			return
		}

		// 简单的 Auth 校验（跳过 /healthz 和 /readyz）
		ctx := r.Context()
		if !strings.HasSuffix(r.URL.Path, "z") && !strings.HasSuffix(r.URL.Path, "metrics") && expectedKey != "" {
			if authManager.IsLocked(clientIP) {
				w.Header().Set("Retry-After", "300")
				http.Error(w, "429 Too Many Requests - Auth Cooldown", http.StatusTooManyRequests)
				return
			}

			// 获取 Token
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == "" {
				token = r.Header.Get("X-API-Key")
			}

			// 使用恒定时间比较防御时序攻击
			if subtle.ConstantTimeCompare([]byte(token), []byte(expectedKey)) != 1 {
				authManager.RecordFailure(clientIP)
				http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
				return
			}

			authManager.RecordSuccess(clientIP)

			// Auth 成功，提取身份并注入 (MVP 阶段由于单一 API Key，统一记录为 admin)
			authCtx := &AuthContext{
				UserID:     "admin",
				ClientType: "api",
			}
			ctx = WithAuthContext(ctx, authCtx)
		} else {
			// 如果没有要求强制认证，或者请求健康检查，标记为匿名
			authCtx := &AuthContext{
				UserID:     "anonymous",
				ClientType: "unknown",
			}
			ctx = WithAuthContext(ctx, authCtx)
		}

		// 仅记录 API 请求（进入时）
		if isAPI {
			slog.Debug("http: request", "method", r.Method, "path", r.URL.Path, "ip", clientIP)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
