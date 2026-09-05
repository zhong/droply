package wework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	defaultRequestTimeout     = 10 * time.Second
	defaultAuthorizeURL       = "https://login.work.weixin.qq.com/wwlogin/sso/login"
	defaultMobileAuthorizeURL = "https://open.weixin.qq.com/connect/oauth2/authorize"
	defaultAPIBaseURL         = "https://qyapi.weixin.qq.com"
)

// Config holds WeWork OAuth configuration.
type Config struct {
	CorpID      string
	AgentID     string
	Secret      string
	RedirectURI string

	// Optional overrides for testing.
	AuthorizeURL       string // PC web QR code login endpoint
	MobileAuthorizeURL string // WeCom in-app OAuth endpoint
	APIBaseURL         string
	HTTPClient         *http.Client
}

// Client wraps WeWork OAuth API calls. Each HTTP request, including its response
// body, has a maximum duration of ten seconds (or a shorter caller deadline).
// User lookups make two sequential requests.
type Client struct {
	corpID             string
	agentID            string
	secret             string
	redirectURI        string
	authorizeURL       string
	mobileAuthorizeURL string
	apiBaseURL         string
	httpClient         *http.Client
}

// NewClient creates a new WeWork OAuth client.
func NewClient(config Config) *Client {
	c := &Client{
		corpID:             config.CorpID,
		agentID:            config.AgentID,
		secret:             config.Secret,
		redirectURI:        config.RedirectURI,
		authorizeURL:       config.AuthorizeURL,
		mobileAuthorizeURL: config.MobileAuthorizeURL,
		apiBaseURL:         config.APIBaseURL,
		httpClient:         config.HTTPClient,
	}
	if c.authorizeURL == "" {
		c.authorizeURL = defaultAuthorizeURL
	}
	if c.mobileAuthorizeURL == "" {
		c.mobileAuthorizeURL = defaultMobileAuthorizeURL
	}
	if c.apiBaseURL == "" {
		c.apiBaseURL = defaultAPIBaseURL
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{}
	}
	// Copy injected clients so setting our bound does not mutate shared callers.
	client := *c.httpClient
	if client.Timeout <= 0 || client.Timeout > defaultRequestTimeout {
		client.Timeout = defaultRequestTimeout
	}
	c.httpClient = &client
	return c
}

// GetAuthorizeURL returns the WeWork QR code login URL for PC web browsers.
// User should be redirected to this URL to scan with WeCom mobile app.
// https://developer.work.weixin.qq.com/document/path/98152
func (c *Client) GetAuthorizeURL(state string) string {
	u, _ := url.Parse(c.authorizeURL)
	q := u.Query()
	q.Set("login_type", "CorpApp")
	q.Set("appid", c.corpID)
	q.Set("agentid", c.agentID)
	q.Set("redirect_uri", c.redirectURI)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String()
}

// GetMobileAuthorizeURL returns the WeWork OAuth URL for users already
// inside the WeCom mobile app (in-app browser). Uses snsapi_base scope to
// silently authorize without showing a QR code.
// https://developer.work.weixin.qq.com/document/path/91022
func (c *Client) GetMobileAuthorizeURL(state string) string {
	u, _ := url.Parse(c.mobileAuthorizeURL)
	q := u.Query()
	q.Set("appid", c.corpID)
	q.Set("redirect_uri", c.redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", "snsapi_base")
	q.Set("agentid", c.agentID)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String() + "#wechat_redirect"
}

// UserInfo represents WeWork user information.
type UserInfo struct {
	UserID string `json:"userid"`
	Name   string `json:"name,omitempty"`
	Mobile string `json:"mobile,omitempty"`
	Email  string `json:"email,omitempty"`
}

// GetAccessToken retrieves the access_token for the agent.
func (c *Client) GetAccessToken(ctx context.Context) (string, error) {
	u := fmt.Sprintf("%s/cgi-bin/gettoken?corpid=%s&corpsecret=%s",
		c.apiBaseURL,
		url.QueryEscape(c.corpID),
		url.QueryEscape(c.secret),
	)

	var result struct {
		ErrCode     int    `json:"errcode"`
		AccessToken string `json:"access_token"`
	}
	if err := c.doGet(ctx, u, &result); err != nil {
		return "", err
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("wework api error: %d", result.ErrCode)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("no access token in response")
	}
	return result.AccessToken, nil
}

// GetUserIDByCode exchanges OAuth code for user_id.
func (c *Client) GetUserIDByCode(ctx context.Context, code string) (string, error) {
	accessToken, err := c.GetAccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("get access token: %w", err)
	}

	u := fmt.Sprintf("%s/cgi-bin/auth/getuserinfo?access_token=%s&code=%s",
		c.apiBaseURL,
		url.QueryEscape(accessToken),
		url.QueryEscape(code),
	)

	var result struct {
		ErrCode int    `json:"errcode"`
		UserID  string `json:"userid"`
	}
	if err := c.doGet(ctx, u, &result); err != nil {
		return "", err
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("wework api error: %d", result.ErrCode)
	}
	if result.UserID == "" {
		return "", fmt.Errorf("no userid in response")
	}
	return result.UserID, nil
}

// GetUserInfo retrieves detailed user information by user_id.
func (c *Client) GetUserInfo(ctx context.Context, userID string) (*UserInfo, error) {
	accessToken, err := c.GetAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	u := fmt.Sprintf("%s/cgi-bin/user/get?access_token=%s&userid=%s",
		c.apiBaseURL,
		url.QueryEscape(accessToken),
		url.QueryEscape(userID),
	)

	var result struct {
		ErrCode int    `json:"errcode"`
		UserID  string `json:"userid"`
		Name    string `json:"name"`
		Mobile  string `json:"mobile"`
		Email   string `json:"email"`
	}
	if err := c.doGet(ctx, u, &result); err != nil {
		return nil, err
	}
	if result.ErrCode != 0 {
		return nil, fmt.Errorf("wework api error: %d", result.ErrCode)
	}
	if result.UserID == "" {
		return nil, fmt.Errorf("no userid in response")
	}
	return &UserInfo{
		UserID: result.UserID,
		Name:   result.Name,
		Mobile: result.Mobile,
		Email:  result.Email,
	}, nil
}

// doGet bounds each upstream round trip, including reading its response body.
func (c *Client) doGet(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("invalid wework request")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// net/http errors can contain the complete URL, including OAuth credentials.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return errors.New("wework request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wework HTTP status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return errors.New("read wework response failed")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("invalid wework JSON response")
	}
	return nil
}
