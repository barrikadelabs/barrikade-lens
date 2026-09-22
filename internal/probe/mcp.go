package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

const mcpProtocolVersion = "2025-11-25"

// MCPHandshake performs only initialize, initialized, and bounded tools/list
// requests. It never sends tool arguments or calls a tool.
func MCPHandshake(ctx context.Context, raw string, config Config) (Result, error) {
	target, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return Result{}, fmt.Errorf("MCP target must be an absolute URL without credentials, query, or fragment")
	}
	host := strings.ToLower(target.Hostname())
	if discovery.IsCloudMetadataHost(host) || !allowed(host, config.AllowedHosts) {
		return Result{}, fmt.Errorf("MCP host is blocked or not allowlisted")
	}
	if config.Timeout <= 0 || config.Timeout > 5*time.Second {
		config.Timeout = 3 * time.Second
	}
	if config.MaxBytes <= 0 || config.MaxBytes > 2<<20 {
		config.MaxBytes = 1 << 20
	}
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	client, err := clientFor(ctx, target, config.Timeout)
	if err != nil {
		return Result{}, fmt.Errorf("MCP endpoint could not be resolved")
	}
	post := func(id int, method string, params any, version, session string) (map[string]json.RawMessage, string, error) {
		message := map[string]any{"jsonrpc": "2.0", "method": method}
		if id > 0 {
			message["id"] = id
		}
		if params != nil {
			message["params"] = params
		}
		body, _ := json.Marshal(message)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
		if err != nil {
			return nil, "", err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		if version != "" {
			request.Header.Set("MCP-Protocol-Version", version)
		}
		if session != "" {
			request.Header.Set("MCP-Session-Id", session)
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, "", fmt.Errorf("MCP request failed")
		}
		defer response.Body.Close()
		if id == 0 && response.StatusCode == http.StatusAccepted {
			return nil, "", nil
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, "", fmt.Errorf("MCP request returned HTTP %d", response.StatusCode)
		}
		mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil {
			return nil, "", fmt.Errorf("MCP response has invalid content type")
		}
		var rawResponse []byte
		switch mediaType {
		case "application/json":
			rawResponse, err = io.ReadAll(io.LimitReader(response.Body, config.MaxBytes+1))
		case "text/event-stream":
			rawResponse, err = readMCPEvent(response.Body, config.MaxBytes, id)
		default:
			return nil, "", fmt.Errorf("MCP response has unsupported content type")
		}
		if err != nil || int64(len(rawResponse)) > config.MaxBytes {
			return nil, "", fmt.Errorf("MCP response exceeded bounds or was incomplete")
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(rawResponse, &envelope); err != nil {
			return nil, "", fmt.Errorf("MCP response is not JSON")
		}
		var returnedID int
		if err := json.Unmarshal(envelope["id"], &returnedID); err != nil || returnedID != id || envelope["error"] != nil {
			return nil, "", fmt.Errorf("MCP response did not match request")
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(envelope["result"], &result); err != nil {
			return nil, "", fmt.Errorf("MCP response has no result")
		}
		return result, response.Header.Get("MCP-Session-Id"), nil
	}
	init, session, err := post(1, "initialize", map[string]any{"protocolVersion": mcpProtocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "barrikade-lens", "version": "2.0.0"}}, "", "")
	if err != nil {
		return Result{}, err
	}
	var version string
	if err := json.Unmarshal(init["protocolVersion"], &version); err != nil || version != mcpProtocolVersion && version != "2025-06-18" && version != "2025-03-26" {
		return Result{}, fmt.Errorf("MCP protocol version is unsupported")
	}
	var capabilities map[string]json.RawMessage
	if err := json.Unmarshal(init["capabilities"], &capabilities); err != nil {
		return Result{}, fmt.Errorf("MCP capabilities are missing")
	}
	if _, ok := capabilities["tools"]; !ok {
		return Result{}, fmt.Errorf("MCP server does not advertise tools")
	}
	if len(session) > 256 || strings.IndexFunc(session, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
		return Result{}, fmt.Errorf("MCP session identifier is invalid")
	}
	if _, _, err := post(0, "notifications/initialized", nil, version, session); err != nil {
		return Result{}, err
	}
	seen := map[string]struct{}{}
	cursor := ""
	for page := 0; page < 4; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		listed, _, err := post(page+2, "tools/list", params, version, session)
		if err != nil {
			return Result{}, err
		}
		var tools []struct {
			Name string `json:"name"`
		}
		if len(listed["tools"]) == 0 || string(listed["tools"]) == "null" {
			return Result{}, fmt.Errorf("MCP tool list is missing")
		}
		if err := json.Unmarshal(listed["tools"], &tools); err != nil {
			return Result{}, fmt.Errorf("MCP tool list is invalid")
		}
		for _, tool := range tools {
			name := strings.TrimSpace(tool.Name)
			if !safeMCPToolName(name) {
				continue
			}
			seen[name] = struct{}{}
			if len(seen) > 100 {
				return Result{}, fmt.Errorf("MCP tool list exceeds 100 names")
			}
		}
		var next string
		_ = json.Unmarshal(listed["nextCursor"], &next)
		if next == "" {
			break
		}
		if len(next) > 256 || next == cursor || page == 3 {
			return Result{}, fmt.Errorf("MCP pagination exceeded bounds")
		}
		cursor = next
	}
	tools := make([]string, 0, len(seen))
	for name := range seen {
		tools = append(tools, name)
	}
	sort.Strings(tools)
	sanitized, _ := discovery.SanitizeURL(target.String())
	name := host
	var serverInfo struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(init["serverInfo"], &serverInfo) == nil && safeMCPToolName(serverInfo.Name) {
		name = serverInfo.Name
	}
	clean, _ := json.Marshal(map[string]any{"version": version, "tools": tools})
	return Result{Kind: discovery.KindMCPServer, Name: name, Endpoint: sanitized, Host: host, ContentHash: discovery.ContentHash(clean), Tools: tools, Attributes: map[string]any{"running_at_scan": true, "transport": "streamable_http", "protocol_version": version, "endpoint": sanitized, "host": host, "capability_state": "observed"}}, nil
}

func safeMCPToolName(name string) bool {
	if name == "" || len(name) > 200 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._:/-", r)) {
			return false
		}
	}
	return true
}

func readMCPEvent(reader io.Reader, maxBytes int64, requestID int) ([]byte, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, maxBytes+1))
	scanner.Buffer(make([]byte, 4096), int(maxBytes)+1)
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(line, "data:"))
			continue
		}
		if line != "" || len(data) == 0 {
			continue
		}
		message := strings.Join(data, "\n")
		data = nil
		var envelope struct {
			ID int `json:"id"`
		}
		if json.Unmarshal([]byte(message), &envelope) == nil && envelope.ID == requestID {
			return []byte(message), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("MCP SSE stream ended without matching response")
}
