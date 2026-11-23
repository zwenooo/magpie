package library

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// The console is a local ops-mcp feature, not its MCP endpoint or OAuth URL.
func opsConsoleBase(s *Server) string {
	if !supportsDirectUse(s) {
		return ""
	}
	value := func(k string) string {
		if v, ok := s.Env[k]; ok {
			return strings.TrimSpace(v)
		}
		return strings.TrimSpace(os.Getenv(k))
	}
	switch strings.ToLower(value("OPS_MCP_CONSOLE")) {
	case "0", "false", "off", "no":
		return ""
	}
	port := 8770
	if raw := value("OPS_MCP_CONSOLE_PORT"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 65535 {
			return ""
		}
		port = n
	}
	return "http://127.0.0.1:" + strconv.Itoa(port)
}

// OpsConsolePage checks the service identity on click. Never proxy private
// logs/configuration, start a worker, follow redirects or accept a supplied URL.
func OpsConsolePage(ctx context.Context, name string, logs bool) (string, error) {
	s, _, err := repoTarget(name, nil)
	if err != nil {
		return "", err
	}
	base := opsConsoleBase(s)
	if base == "" {
		return "", errors.New("ops-mcp console not configured")
	}
	return checkedConsolePage(ctx, base, logs)
}
func checkedConsolePage(ctx context.Context, base string, logs bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/api/ping", nil)
	if err != nil {
		return "", err
	}
	tr := &http.Transport{DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	cl := http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := cl.Do(req)
	if err != nil {
		return "", errors.New("ops-mcp console unavailable")
	}
	defer res.Body.Close()
	var reply struct {
		Service string `json:"service"`
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 4097))
	if err != nil || len(b) > 4096 || res.StatusCode != 200 || json.Unmarshal(b, &reply) != nil || reply.Service != "ops-mcp-config-console" {
		return "", errors.New("could not verify ops-mcp console")
	}
	if logs {
		return base + "/logs", nil
	}
	return base + "/", nil
}
