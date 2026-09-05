package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// APIClient wraps an HTTP client with auth and base URL for the droply API.
type APIClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewAPIClient creates an APIClient from the given context.
func NewAPIClient(ctx *Context) *APIClient {
	return &APIClient{
		BaseURL: ctx.APIURL,
		Token:   ctx.Token,
		HTTP:    &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

// doJSON performs a JSON request against the API. If body is non-nil it is
// marshalled as the request body. The response is decoded into result if
// non-nil. An error is returned for non-2xx responses.
func (c *APIClient) doJSON(method, path string, body any, result any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp struct {
			Error        string `json:"error"`
			DeploymentID int64  `json:"deployment_id"`
			Version      int    `json:"version"`
		}
		if jsonErr := json.Unmarshal(respBytes, &errResp); jsonErr == nil {
			if msg := errResp.Error; msg != "" {
				return fmt.Errorf("API error: %s", msg)
			}
		}
		return fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	if result != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// uploadFile uploads filePath as a multipart form to the given API path.
func (c *APIClient) uploadFile(path, filePath string) (map[string]any, error) {
	return c.uploadFileContext(context.Background(), path, filePath)
}

func (c *APIClient) uploadFileContext(ctx context.Context, path, filePath string) (map[string]any, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, fmt.Errorf("copy file: %w", err)
	}
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	// An upload has no idempotency key: never replay it after a redirect or ambiguous failure.
	req.GetBody = nil
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp struct {
			Error        string `json:"error"`
			DeploymentID int64  `json:"deployment_id"`
			Version      int    `json:"version"`
		}
		if jsonErr := json.Unmarshal(respBytes, &errResp); jsonErr == nil {
			if msg := errResp.Error; msg != "" {
				return nil, fmt.Errorf("API error: %s", msg)
			}
		}
		return nil, fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}
