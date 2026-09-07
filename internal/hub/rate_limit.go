package hub

import (
	"crypto/sha256"
	"net"
	"net/http"
	"strconv"
	"time"
)

type requestLimitKey func(*http.Request) string

func remoteRequestKey(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return request.RemoteAddr
}

func principalRequestKey(request *http.Request) string {
	principal, ok := principalFrom(request.Context())
	if !ok {
		return remoteRequestKey(request)
	}
	if principal.SourceID != "" {
		return principal.OrganizationID + ":" + principal.SourceID
	}
	return principal.OrganizationID
}

// rateLimit uses PostgreSQL so limits remain consistent across Hub replicas.
// The digest keeps source, workspace, and network identifiers out of this
// operational table. The deployment edge remains the first line of defense.
func (s *Server) rateLimit(scope string, limit int, window time.Duration, key requestLimitKey, next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		now := time.Now().UTC()
		windowStart := now.Truncate(window)
		digest := sha256.Sum256([]byte(key(request)))
		var count int
		err := s.config.WorkerPool.QueryRow(request.Context(), `INSERT INTO request_rate_limits(scope,key_digest,window_start,request_count,expires_at)
			VALUES($1,$2,$3,1,$4)
			ON CONFLICT(scope,key_digest,window_start) DO UPDATE SET request_count=request_rate_limits.request_count+1
			RETURNING request_count`, scope, digest[:], windowStart, windowStart.Add(2*window)).Scan(&count)
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "rate_limit_unavailable", "Request protection is temporarily unavailable")
			return
		}
		if count > limit {
			retryAfter := int(time.Until(windowStart.Add(window)).Seconds()) + 1
			if retryAfter < 1 {
				retryAfter = 1
			}
			writer.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			writeError(writer, http.StatusTooManyRequests, "rate_limited", "Too many requests; retry after the current window")
			return
		}
		next(writer, request)
	}
}
