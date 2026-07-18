package hub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WebhookWorker struct {
	Pool         *pgxpool.Pool
	Logger       *slog.Logger
	PollInterval time.Duration
}

func (w WebhookWorker) Run(ctx context.Context) error {
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.PollInterval == 0 {
		w.PollInterval = time.Second
	}
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if processed, err := w.processOne(ctx); err != nil {
				w.Logger.Warn("webhook delivery failed", "error", err)
			} else if processed {
				continue
			}
		}
	}
}

func (w WebhookWorker) processOne(ctx context.Context) (bool, error) {
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var deliveryID uuid.UUID
	var endpoint, event string
	var payload, secret []byte
	var attempts int
	err = tx.QueryRow(ctx, `SELECT o.id,e.url,o.event_type,o.payload,e.signing_secret,o.attempts FROM webhook_outbox o JOIN webhook_endpoints e ON e.id=o.webhook_id WHERE o.delivered_at IS NULL AND o.next_attempt_at<=now() AND e.enabled=true ORDER BY o.next_attempt_at FOR UPDATE OF o SKIP LOCKED LIMIT 1`).Scan(&deliveryID, &endpoint, &event, &payload, &secret, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	status, deliveryErr := deliverWebhook(ctx, deliveryID, event, endpoint, payload, secret)
	if deliveryErr == nil && status >= 200 && status < 300 {
		_, err = tx.Exec(ctx, `UPDATE webhook_outbox SET delivered_at=now(),last_status=$2,attempts=attempts+1 WHERE id=$1`, deliveryID, status)
	} else {
		delay := time.Duration(1<<min(attempts, 10)) * time.Second
		if delay > time.Hour {
			delay = time.Hour
		}
		_, err = tx.Exec(ctx, `UPDATE webhook_outbox SET last_status=$2,attempts=attempts+1,next_attempt_at=now()+$3::interval WHERE id=$1`, deliveryID, status, fmt.Sprintf("%f seconds", delay.Seconds()))
	}
	if err != nil {
		return true, err
	}
	if err = tx.Commit(ctx); err != nil {
		return true, err
	}
	return true, deliveryErr
}

func deliverWebhook(ctx context.Context, id uuid.UUID, event, endpoint string, payload, secret []byte) (int, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return 0, err
	}
	client, err := restrictedHTTPClient(ctx, parsed)
	if err != nil {
		return 0, err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(payload)
	signature := hex.EncodeToString(mac.Sum(nil))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "barrikade-lens-hub/"+Version)
	request.Header.Set("X-Lens-Event", event)
	request.Header.Set("X-Lens-Delivery", id.String())
	request.Header.Set("X-Lens-Signature", "t="+timestamp+",v1="+signature)
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, fmt.Errorf("webhook returned HTTP %d", response.StatusCode)
	}
	return response.StatusCode, nil
}

func restrictedHTTPClient(ctx context.Context, endpoint *url.URL) (*http.Client, error) {
	host := endpoint.Hostname()
	if discovery.IsCloudMetadataHost(host) {
		return nil, fmt.Errorf("metadata endpoint is blocked")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	allowed := []net.IP{}
	for _, address := range addresses {
		if address.IP.IsLoopback() || address.IP.IsPrivate() || address.IP.IsLinkLocalUnicast() || address.IP.IsUnspecified() {
			continue
		}
		allowed = append(allowed, address.IP)
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("webhook host resolves only to blocked addresses")
	}
	port := endpoint.Port()
	if port == "" {
		port = "443"
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		var last error
		for _, ip := range allowed {
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return connection, nil
			}
			last = err
		}
		return nil, last
	}, ForceAttemptHTTP2: true}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
