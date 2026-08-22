// Package ai_agent 提供 Gin 后端访问独立 AI Agent 服务的内部基础设施。
//
// 该包不注册 Gin 路由，也不依赖浏览器 Cookie/JWT。调用方应传入
// 服务间身份凭据和已经鉴权、裁剪的用户上下文。
package ai_agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

const (
	defaultMaxResponseBytes = 2 << 20 // 2 MiB
	defaultMaxEventBytes    = 1 << 20 // 1 MiB
	maxErrorBodyBytes       = 64 << 10
)

// Config 描述 AI Agent 内网客户端。BaseURL 例如 http://ai-agent:8080。
type Config struct {
	BaseURL          string
	ServiceToken     string
	HTTPClient       *http.Client
	MaxResponseBytes int64
	MaxEventBytes    int
}

// Client 是并发安全的 AI Agent 内网客户端。
type Client struct {
	baseURL          *url.URL
	serviceToken     string
	httpClient       *http.Client
	maxResponseBytes int64
	maxEventBytes    int
}

// NewClient 创建 AI Agent 客户端。空 ServiceToken 便于本地 mTLS 环境；
// 生产环境应通过 ServiceToken 或自定义 mTLS Transport 提供服务身份。
func NewClient(config Config) (*Client, error) {
	baseURL, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil {
		return nil, fmt.Errorf("ai_agent: 解析 BaseURL: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, errors.New("ai_agent: BaseURL 必须使用 http 或 https")
	}
	if baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("ai_agent: BaseURL 格式无效")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	maxEventBytes := config.MaxEventBytes
	if maxEventBytes <= 0 {
		maxEventBytes = defaultMaxEventBytes
	}

	return &Client{
		baseURL:          baseURL,
		serviceToken:     strings.TrimSpace(config.ServiceToken),
		httpClient:       httpClient,
		maxResponseBytes: maxResponseBytes,
		maxEventBytes:    maxEventBytes,
	}, nil
}

type Actor struct {
	UserID   string   `json:"userId"`
	TenantID string   `json:"tenantId,omitempty"`
	Roles    []string `json:"roles,omitempty"`
}

type Limits struct {
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	EnabledTools    []string `json:"enabledTools,omitempty"`
}

type CreateRunRequest struct {
	ExternalRunID string         `json:"externalRunId"`
	Input         string         `json:"input"`
	Model         string         `json:"model,omitempty"`
	Attachments   []string       `json:"attachments,omitempty"`
	Actor         Actor          `json:"actor"`
	Limits        Limits         `json:"limits,omitempty"`
	TraceID       string         `json:"traceId,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunCancelled RunStatus = "cancelled"
)

type Run struct {
	ID            string         `json:"id"`
	ExternalRunID string         `json:"externalRunId,omitempty"`
	Status        RunStatus      `json:"status"`
	CreatedAt     int64          `json:"createdAt,omitempty"`
	UpdatedAt     int64          `json:"updatedAt,omitempty"`
	Error         *RunError      `json:"error,omitempty"`
	Usage         *Usage         `json:"usage,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type RunError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
}

// HTTPError 表示 Agent 返回了非 2xx 状态。Body 最多保留 64 KiB。
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("ai_agent: Agent 返回 HTTP %d: %s", e.StatusCode, e.Body)
}

func (c *Client) CreateRun(ctx context.Context, input CreateRunRequest) (*Run, error) {
	var run Run
	if err := c.doJSON(ctx, http.MethodPost, "/v1/runs", input, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (c *Client) GetRun(ctx context.Context, runID string) (*Run, error) {
	var run Run
	if err := c.doJSON(ctx, http.MethodGet, runPath(runID), nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// CancelRun 请求 Agent 取消 Run。Agent 可返回 200 JSON 或 204。
func (c *Client) CancelRun(ctx context.Context, runID string) (*Run, error) {
	request, err := c.newRequest(ctx, http.MethodPost, runPath(runID)+"/cancel", nil)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("ai_agent: 取消 Run: %w", err)
	}
	defer response.Body.Close()
	if err := checkResponse(response); err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	var run Run
	if err := decodeJSON(response.Body, c.maxResponseBytes, &run); err != nil {
		return nil, fmt.Errorf("ai_agent: 解析取消结果: %w", err)
	}
	return &run, nil
}

// StreamEvents 订阅 Agent 的内部 SSE 事件流。afterEventID 会同时作为
// after query 和 Last-Event-ID 头传递，便于 Agent 从持久化游标续传。
func (c *Client) StreamEvents(ctx context.Context, runID, afterEventID string) (*EventStream, error) {
	request, err := c.newRequest(ctx, http.MethodGet, runPath(runID)+"/events", nil)
	if err != nil {
		return nil, err
	}
	query := request.URL.Query()
	if afterEventID != "" {
		query.Set("after", afterEventID)
		request.Header.Set("Last-Event-ID", afterEventID)
	}
	request.URL.RawQuery = query.Encode()
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Cache-Control", "no-cache")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("ai_agent: 订阅事件流: %w", err)
	}
	if err := checkResponse(response); err != nil {
		response.Body.Close()
		return nil, err
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		response.Body.Close()
		return nil, fmt.Errorf("ai_agent: 事件流 Content-Type 无效: %q", response.Header.Get("Content-Type"))
	}
	return newEventStream(response.Body, c.maxEventBytes), nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("ai_agent: 序列化请求: %w", err)
		}
		body = bytes.NewReader(data)
	}
	request, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("ai_agent: 请求 Agent: %w", err)
	}
	defer response.Body.Close()
	if err := checkResponse(response); err != nil {
		return err
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := decodeJSON(response.Body, c.maxResponseBytes, output); err != nil {
		return fmt.Errorf("ai_agent: 解析 Agent 响应: %w", err)
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	target := strings.TrimRight(c.baseURL.String(), "/") + path
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("ai_agent: 创建请求: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if c.serviceToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.serviceToken)
	}
	return request, nil
}

func runPath(runID string) string {
	return "/v1/runs/" + url.PathEscape(runID)
}

func checkResponse(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
	return &HTTPError{StatusCode: response.StatusCode, Body: strings.TrimSpace(string(body))}
}

func decodeJSON(body io.Reader, limit int64, output any) error {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errors.New("Agent 响应超过大小限制")
	}
	return json.Unmarshal(data, output)
}
