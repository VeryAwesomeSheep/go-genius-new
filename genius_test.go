package genius

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func assertEqual[T comparable](t *testing.T, got T, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func Test_DefaultConfig(t *testing.T) {
	t.Setenv("GENIUS_ACCESS_TOKEN", "test-token-from-env")

	config := DefaultConfig()

	assertEqual(t, config.BaseURL, defaultBaseURL)
	assertEqual(t, config.Token, "test-token-from-env")
	assertEqual(t, config.UserAgent, defaultUserAgent)

	if config.HTTP == nil {
		t.Fatal("HTTP Client is nil")
	}
	assertEqual(t, config.HTTP.Timeout, 5*time.Second)
}

func Test_NewClient(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		envToken  string
		wantURL   string
		wantToken string
		wantUA    string
		wantErr   bool
	}{
		{
			name:      "Default (nil) config",
			cfg:       nil,
			envToken:  "env-token",
			wantURL:   defaultBaseURL,
			wantToken: "env-token",
			wantUA:    defaultUserAgent,
			wantErr:   false,
		},
		{
			name: "Custom BaseURL, Token and UserAgent",
			cfg: &Config{
				BaseURL:   "https://example.com/",
				Token:     "custom-token",
				HTTP:      &http.Client{Timeout: 10 * time.Second},
				UserAgent: "custom-ua/v2",
			},
			envToken:  "env-token",
			wantURL:   "https://example.com/",
			wantToken: "custom-token",
			wantUA:    "custom-ua/v2",
			wantErr:   false,
		},
		{
			name: "BaseURL missing trailing slash error",
			cfg: &Config{
				BaseURL: "https://example.com",
				Token:   "token",
			},
			envToken: "env-token",
			wantErr:  true,
		},
		{
			name: "Missing token error",
			cfg: &Config{
				Token: "",
			},
			envToken: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GENIUS_ACCESS_TOKEN", tt.envToken)

			c, err := NewClient(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				assertEqual(t, c.baseURL.String(), tt.wantURL)
				assertEqual(t, c.token, tt.wantToken)
				assertEqual(t, c.userAgent, tt.wantUA)

				if c.common.client != c {
					t.Error("NewClient() common.client does not point back to the Client")
				}

				if tt.cfg != nil && tt.cfg.HTTP != nil {
					if c.http != tt.cfg.HTTP {
						t.Error("NewClient() did not reuse the injected HTTP client")
					}
					assertEqual(t, c.http.Timeout, tt.cfg.HTTP.Timeout)
				}
			}
		})
	}
}

func Test_NewRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     *Config
		path    string
		wantURL string
		wantErr bool
	}{
		{
			name:    "Basic GET",
			cfg:     &Config{Token: "test-token", UserAgent: "test-ua"},
			path:    "songs/123",
			wantURL: "https://api.genius.com/songs/123",
			wantErr: false,
		},
		{
			name:    "Invalid path",
			cfg:     &Config{Token: "test-token", UserAgent: "test-ua"},
			path:    "%%",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := NewClient(tt.cfg)
			req, err := c.NewRequest(tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewRequest() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				assertEqual(t, req.Method, http.MethodGet)
				assertEqual(t, req.URL.String(), tt.wantURL)
				assertEqual(t, req.Header.Get("Accept"), "application/json")
				assertEqual(t, req.Header.Get("User-Agent"), tt.cfg.UserAgent)
				assertEqual(t, req.Header.Get("Authorization"), "Bearer test-token")
			}
		})
	}

	t.Run("Invalid baseURL", func(t *testing.T) {
		t.Parallel()
		badURL, _ := url.Parse("https://api.genius.com/api") // No trailing slash
		client := &Client{baseURL: badURL}
		_, err := client.NewRequest("test")
		if err == nil {
			t.Error("NewRequest() expected error for baseURL without trailing slash")
		}
	})
}

func Test_Do(t *testing.T) {
	type testResponse struct {
		Field string `json:"field"`
	}

	tests := []struct {
		name           string
		handler        http.HandlerFunc
		ctx            context.Context
		v              any
		wantResult     string
		wantErr        bool
		wantStatusCode int // 0 means don't check
	}{
		{
			name: "Success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"field": "value"}`)
			},
			ctx:            context.Background(),
			v:              &testResponse{},
			wantResult:     "value",
			wantErr:        false,
			wantStatusCode: http.StatusOK,
		},
		{
			name: "Nil v skips decoding",
			handler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"field": "value"}`)
			},
			ctx:            context.Background(),
			v:              nil,
			wantErr:        false,
			wantStatusCode: http.StatusOK,
		},
		{
			name: "API Error (404)",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			ctx:            context.Background(),
			v:              &testResponse{},
			wantErr:        true,
			wantStatusCode: http.StatusNotFound,
		},
		{
			name: "Invalid JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `invalid-json`)
			},
			ctx:            context.Background(),
			v:              &testResponse{},
			wantErr:        true,
			wantStatusCode: http.StatusOK,
		},
		{
			name: "Empty body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			ctx:            context.Background(),
			v:              &testResponse{},
			wantErr:        false,
			wantStatusCode: http.StatusOK,
		},
		{
			name: "Context cancellation",
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(100 * time.Millisecond)
				fmt.Fprint(w, `{"field": "value"}`)
			},
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			}(),
			v:       &testResponse{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			c, _ := NewClient(&Config{Token: "test", BaseURL: srv.URL + "/"})
			req, _ := c.NewRequest("/")
			resp, err := c.Do(tt.ctx, req, tt.v)
			if resp != nil && resp.Body != nil {
				defer resp.Body.Close()
			}

			if (err != nil) != tt.wantErr {
				t.Fatalf("Do() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr && tt.v != nil {
				if res, ok := tt.v.(*testResponse); ok {
					assertEqual(t, res.Field, tt.wantResult)
				}
			}

			if tt.wantStatusCode != 0 {
				if resp == nil {
					t.Fatal("Do() returned a nil *http.Response")
				}
				assertEqual(t, resp.StatusCode, tt.wantStatusCode)
			}
		})
	}
}

func Test_Error(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusNotFound}
	err := &ErrorResponse{Response: resp, Message: "not found"}
	assertEqual(t, err.Error(), "status: 404, message: not found")
}

func Test_CheckResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		statusCode  int
		body        string
		wantErr     bool
		wantMessage string
	}{
		{
			name:       "100 Continue is an error",
			statusCode: http.StatusContinue,
			wantErr:    true,
		},
		{
			name:       "200 OK is not an error",
			statusCode: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "299 is not an error",
			statusCode: 299,
			wantErr:    false,
		},
		{
			name:       "300 Multiple Choices is an error",
			statusCode: http.StatusMultipleChoices,
			wantErr:    true,
		},
		{
			name:        "404 with JSON message",
			statusCode:  http.StatusNotFound,
			body:        `{"meta": {"message": "not found"}}`,
			wantErr:     true,
			wantMessage: "not found",
		},
		{
			name:       "500 with empty body",
			statusCode: http.StatusInternalServerError,
			body:       "",
			wantErr:    true,
		},
		{
			name:        "500 with malformed JSON body",
			statusCode:  http.StatusInternalServerError,
			body:        `not-json`,
			wantErr:     true,
			wantMessage: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}

			err := CheckResponse(resp)

			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckResponse() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				errResp, ok := err.(*ErrorResponse)
				if !ok {
					t.Fatalf("CheckResponse() error type = %T, want *ErrorResponse", err)
				}
				assertEqual(t, errResp.Response.StatusCode, tt.statusCode)
				assertEqual(t, errResp.Message, tt.wantMessage)
			}
		})
	}
}
