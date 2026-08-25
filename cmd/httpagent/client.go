package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 32 << 20

type apiClient struct {
	baseURL string
	http    *http.Client
}

type apiError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("HTTP %d %s: %s", e.StatusCode, e.Code, e.Message)
}

func newAPIClient(baseURL string, timeout time.Duration) (*apiClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("invalid simulator URL %q", baseURL)
	}
	return &apiClient{baseURL: baseURL, http: &http.Client{Timeout: timeout}}, nil
}

func (c *apiClient) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read %s %s response: %w", method, path, err)
	}
	if len(payload) > maxResponseBytes {
		return fmt.Errorf("%s %s response is larger than %d bytes", method, path, maxResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		apiBody := apiErrorBody{Message: strings.TrimSpace(string(payload))}
		_ = json.Unmarshal(payload, &apiBody)
		return &apiError{StatusCode: response.StatusCode, Code: apiBody.Error, Message: apiBody.Message}
	}
	if output != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, output); err != nil {
			return fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return nil
}

func (c *apiClient) start(ctx context.Context, input startRunRequest) (startRunResponse, error) {
	var result startRunResponse
	err := c.do(ctx, http.MethodPost, "/v1/start", input, &result)
	return result, err
}

func (c *apiClient) overview(ctx context.Context, runID string) (overviewResponse, error) {
	var result overviewResponse
	err := c.do(ctx, http.MethodGet, runPath(runID, "/overview"), nil, &result)
	return result, err
}

func (c *apiClient) logs(ctx context.Context, runID string, from, to time.Time) ([]requestLog, error) {
	result := make([]requestLog, 0)
	cursor := ""
	for {
		query := url.Values{"from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)}, "status": {"500"}, "limit": {"1000"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var page logsResponse
		if err := c.do(ctx, http.MethodGet, runPath(runID, "/logs")+"?"+query.Encode(), nil, &page); err != nil {
			return nil, err
		}
		result = append(result, page.Logs...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			return result, nil
		}
		cursor = *page.NextCursor
	}
}

func (c *apiClient) deployments(ctx context.Context, runID string) (deploymentsResponse, error) {
	var result deploymentsResponse
	err := c.do(ctx, http.MethodGet, runPath(runID, "/deployments"), nil, &result)
	return result, err
}

func (c *apiClient) startDeployment(ctx context.Context, runID, requestID, deploymentID string) error {
	return c.do(ctx, http.MethodPost, runPath(runID, "/deployments"), map[string]any{
		"request_id": requestID, "deployment_id": deploymentID,
	}, nil)
}

func (c *apiClient) applyFix(ctx context.Context, runID, requestID, message string) error {
	return c.do(ctx, http.MethodPost, runPath(runID, "/fixes"), map[string]any{
		"request_id": requestID, "message": message,
	}, nil)
}

func (c *apiClient) resources(ctx context.Context, runID string) (resourcesResponse, error) {
	var result resourcesResponse
	err := c.do(ctx, http.MethodGet, runPath(runID, "/resources"), nil, &result)
	return result, err
}

func (c *apiClient) metrics(ctx context.Context, runID string) (metricsResponse, error) {
	var result metricsResponse
	err := c.do(ctx, http.MethodGet, runPath(runID, "/metrics"), nil, &result)
	return result, err
}

func (c *apiClient) scale(ctx context.Context, runID, requestID string, desired int) error {
	return c.do(ctx, http.MethodPut, runPath(runID, "/resources/backend"), map[string]any{
		"request_id": requestID, "desired_instances": desired,
	}, nil)
}

func (c *apiClient) advance(ctx context.Context, runID, requestID string, duration time.Duration) (advanceTimeResponse, error) {
	var result advanceTimeResponse
	err := c.do(ctx, http.MethodPost, runPath(runID, "/time/advance"), map[string]any{
		"request_id": requestID, "duration_seconds": int64(duration / time.Second),
	}, &result)
	return result, err
}

func (c *apiClient) economy(ctx context.Context, runID string) (economyResponse, error) {
	var result economyResponse
	err := c.do(ctx, http.MethodGet, runPath(runID, "/economy"), nil, &result)
	return result, err
}

func runPath(runID, suffix string) string {
	return "/v1/runs/" + url.PathEscape(runID) + suffix
}
