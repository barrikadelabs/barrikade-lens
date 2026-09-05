package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func doJSON(ctx context.Context, client HTTPDoer, method, endpoint, bearer string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Code: "provider_unavailable", Message: "The provider API could not be reached", Retryable: true, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerHTTPError(response)
	}
	if target == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(target); err != nil {
		return &Error{Code: "malformed_provider_response", Message: "The provider returned an unreadable response", Cause: err}
	}
	return nil
}

func providerHTTPError(response *http.Response) error {
	retryAfter := parseRetryAfter(response.Header.Get("Retry-After"))
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return &Error{Code: "authentication_failed", Message: "The provider did not accept Lens's temporary identity"}
	case http.StatusForbidden:
		return &Error{Code: "permission_denied", Message: "The connected role is missing a required read permission"}
	case http.StatusNotFound:
		return &Error{Code: "api_unavailable", Message: "This provider API is unavailable in the selected scope"}
	case http.StatusTooManyRequests:
		return &Error{Code: "provider_throttled", Message: "The provider temporarily throttled this detector", Retryable: true, RetryAfter: retryAfter}
	default:
		if response.StatusCode >= 500 {
			return &Error{Code: "provider_unavailable", Message: "The provider API is temporarily unavailable", Retryable: true, RetryAfter: retryAfter}
		}
		return &Error{Code: "provider_request_failed", Message: fmt.Sprintf("The provider returned HTTP %d", response.StatusCode)}
	}
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil && when.After(time.Now()) {
		return time.Until(when)
	}
	return 0
}
