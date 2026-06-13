package wework

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const (
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

// Client wraps WeWork OAuth API calls.
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
		c.httpClient = http.DefaultClient
	}
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
func (c *Client) GetAccessToken() (string, error) {
	u := fmt.Sprintf("%s/cgi-bin/gettoken?corpid=%s&corpsecret=%s",
		c.apiBaseURL,
		url.QueryEscape(c.corpID),
		url.QueryEscape(c.secret),
	)

	var result struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := c.doGet(u, &result); err != nil {
		return "", err
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("wework api error: %d %s", result.ErrCode, result.ErrMsg)
	}
	return result.AccessToken, nil
}

// GetUserIDByCode exchanges OAuth code for user_id.
func (c *Client) GetUserIDByCode(code string) (string, error) {
	accessToken, err := c.GetAccessToken()
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
		ErrMsg  string `json:"errmsg"`
		UserID  string `json:"userid"`
	}
	if err := c.doGet(u, &result); err != nil {
		return "", err
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("wework api error: %d %s", result.ErrCode, result.ErrMsg)
	}
	if result.UserID == "" {
		return "", fmt.Errorf("no userid in response")
	}
	return result.UserID, nil
}

// GetUserInfo retrieves detailed user information by user_id.
func (c *Client) GetUserInfo(userID string) (*UserInfo, error) {
	accessToken, err := c.GetAccessToken()
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
		ErrMsg  string `json:"errmsg"`
		UserID  string `json:"userid"`
		Name    string `json:"name"`
		Mobile  string `json:"mobile"`
		Email   string `json:"email"`
	}
	if err := c.doGet(u, &result); err != nil {
		return nil, err
	}
	if result.ErrCode != 0 {
		return nil, fmt.Errorf("wework api error: %d %s", result.ErrCode, result.ErrMsg)
	}
	return &UserInfo{
		UserID: result.UserID,
		Name:   result.Name,
		Mobile: result.Mobile,
		Email:  result.Email,
	}, nil
}

func (c *Client) doGet(url string, out interface{}) error {
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	return nil
}
