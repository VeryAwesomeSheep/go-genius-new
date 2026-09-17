package genius

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	version          = "v0.1.0"
	defaultUserAgent = "go-genius" + "/" + version
	defaultBaseURL   = "https://api.genius.com/"
)

// Config specifies Client configuration.
type Config struct {
	BaseURL   string
	Token     string
	HTTP      *http.Client
	UserAgent string
}

// DefaultConfig creates a default configuration for Client.
func DefaultConfig() *Config {
	config := &Config{
		BaseURL:   defaultBaseURL,
		Token:     os.Getenv("GENIUS_ACCESS_TOKEN"),
		HTTP:      &http.Client{Timeout: 5 * time.Second},
		UserAgent: defaultUserAgent,
	}

	return config
}

// Client is a Genius API client that provides basis for accessing Genius API.
type Client struct {
	http      *http.Client
	baseURL   *url.URL
	userAgent string
	token     string

	common Service // reuse a single Client copy for all services
}

// Service is the common service struct that holds a reference to the Client.
type Service struct {
	client *Client
}

// NewClient creates a new Genius API client.
func NewClient(cfg *Config) (*Client, error) {
	config := DefaultConfig()

	// Overwrite default config with user defined values
	if cfg != nil {
		if cfg.BaseURL != "" {
			config.BaseURL = cfg.BaseURL
		}
		if cfg.Token != "" {
			config.Token = cfg.Token
		}
		if cfg.HTTP != nil {
			config.HTTP = cfg.HTTP
		}
		if cfg.UserAgent != "" {
			config.UserAgent = cfg.UserAgent
		}
	}

	if !strings.HasSuffix(config.BaseURL, "/") {
		return nil, fmt.Errorf("baseURL must have a trailing slash, but %q does not", config.BaseURL)
	}
	if config.Token == "" {
		return nil, fmt.Errorf("missing API token")
	}

	c := &Client{}

	c.http = config.HTTP
	c.baseURL, _ = url.Parse(config.BaseURL)
	c.userAgent = config.UserAgent
	c.token = config.Token

	// Create services
	c.common.client = c

	return c, nil
}

// NewRequest performs basic API request preparation.
func (c *Client) NewRequest(path string) (*http.Request, error) {
	if !strings.HasSuffix(c.baseURL.Path, "/") {
		return nil, fmt.Errorf("baseURL must have a trailing slash, but %q does not", c.baseURL)
	}

	u, err := c.baseURL.Parse(path)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", c.token))

	return req, nil
}

// Response is a Genius API response. This wraps the standard http.Response
// and provides the decoded data.
type Response[T any] struct {
	Meta struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
	} `json:"meta"`
	Response T `json:"response"`
}

// Do sends an API request and returns the API response. The API response is
// JSON decoded and stored in the value pointed to by v, or returned as an
// error if an API error occurred.
func (c *Client) Do(ctx context.Context, req *http.Request, v any) (*http.Response, error) {
	req = req.WithContext(ctx)

	resp, err := c.http.Do(req)
	if err != nil {
		return resp, err
	}
	defer resp.Body.Close()

	err = CheckResponse(resp)
	if err != nil {
		return resp, err
	}

	if v != nil {
		decErr := json.NewDecoder(resp.Body).Decode(v)
		if decErr == io.EOF {
			decErr = nil // ignore EOF caused by empty response body
		}

		if decErr != nil {
			err = decErr
			return resp, err
		}
	}

	return resp, nil
}

// ErrorResponse reports an error caused by an API request.
type ErrorResponse struct {
	Response *http.Response `json:"-"`
	Message  string         `json:"message"`
}

func (r *ErrorResponse) Error() string {
	return fmt.Sprintf("status: %v, message: %v", r.Response.StatusCode, r.Message)
}

// CheckResponse checks the API response for errors, and returns them if
// present. A response is considered an error if it has a status code outside
// the 200 range.
func CheckResponse(r *http.Response) error {
	if r.StatusCode >= 200 && r.StatusCode <= 299 {
		return nil
	}

	errorResponse := &Response[any]{}
	data, err := io.ReadAll(r.Body)
	if err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, errorResponse)
	}

	return &ErrorResponse{r, errorResponse.Meta.Message}
}
