package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Client.WithHTTPClient
// =============================================================================

func TestClient_WithHTTPClient(t *testing.T) {
	mock := &mockHTTPDoer{
		doFn: func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		},
	}

	client := NewClient(WithHTTPClient(mock))
	// NewClient wraps in RetryDoer, so check the inner
	rd, ok := client.HTTPClient.(*RetryDoer)
	if !ok {
		t.Fatal("expected RetryDoer wrapping")
	}
	if rd.inner != mock {
		t.Error("WithHTTPClient should set the inner HTTPDoer to the mock")
	}
}

// =============================================================================
// Client.WithTimeout - edge case: non-*http.Client
// =============================================================================

type customDoer struct{}

func (c *customDoer) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
}

func TestClient_WithTimeout_NonHTTPClient(t *testing.T) {
	// When HTTPClient is not an *http.Client, WithTimeout replaces it entirely
	c := &Client{HTTPClient: &customDoer{}}
	opt := WithTimeout(5 * time.Second)
	opt(c)

	hc, ok := c.HTTPClient.(*http.Client)
	if !ok {
		t.Fatal("expected *http.Client after WithTimeout on non-*http.Client")
	}
	if hc.Timeout != 5*time.Second {
		t.Errorf("expected timeout 5s, got %v", hc.Timeout)
	}
}

// =============================================================================
// Client.WithAction - empty action
// =============================================================================

func TestWithAction_Empty(t *testing.T) {
	got := WithAction("/me/drive/items/123", "")
	if got != "/me/drive/items/123" {
		t.Errorf("expected original path, got %q", got)
	}
}

// =============================================================================
// Client.URL - edge cases
// =============================================================================

func TestClient_URL_EdgeCases(t *testing.T) {
	client := &Client{BaseURL: "https://graph.microsoft.com/v1.0"}

	t.Run("path without leading slash", func(t *testing.T) {
		got := client.URL("me/drive/root", nil)
		want := "https://graph.microsoft.com/v1.0/me/drive/root"
		if got != want {
			t.Errorf("expected %q, got %q", want, got)
		}
	})

	t.Run("with query params", func(t *testing.T) {
		q := url.Values{"$top": []string{"10"}}
		got := client.URL("/me/drive/root/children", q)
		if !strings.Contains(got, "?%24top=10") {
			t.Errorf("expected query params in URL, got %q", got)
		}
	})

	t.Run("base URL with trailing slash", func(t *testing.T) {
		client2 := &Client{BaseURL: "https://graph.microsoft.com/v1.0/"}
		got := client2.URL("/me/drive/root", nil)
		if strings.HasSuffix(got, "//") {
			t.Errorf("double slash in URL: %q", got)
		}
	})
}

// =============================================================================
// GraphError.Is - full coverage
// =============================================================================

func TestGraphError_Is(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		target     error
		want       bool
	}{
		{"NotFound matches ErrItemNotFound", http.StatusNotFound, ErrItemNotFound, true},
		{"Unauthorized matches ErrInvalidToken", http.StatusUnauthorized, ErrInvalidToken, true},
		{"429 matches ErrThrottled", http.StatusTooManyRequests, ErrThrottled, true},
		{"409 matches ErrConflict", http.StatusConflict, ErrConflict, true},
		{"404 does not match ErrInvalidToken", http.StatusNotFound, ErrInvalidToken, false},
		{"401 does not match ErrItemNotFound", http.StatusUnauthorized, ErrItemNotFound, false},
		{"500 does not match anything", http.StatusInternalServerError, ErrItemNotFound, false},
		{"200 does not match anything", http.StatusOK, ErrThrottled, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ge := &GraphError{StatusCode: tt.statusCode}
			got := ge.Is(tt.target)
			if got != tt.want {
				t.Errorf("Is(%v) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

// =============================================================================
// checkResponse - JSON without error field
// =============================================================================

func TestCheckResponse_JSONWithoutErrorField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"some_other_field": "value"}`))
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("failed to get: %v", err)
	}
	defer resp.Body.Close()

	err = checkResponse(resp)
	if err == nil {
		t.Fatal("expected error for JSON without error field")
	}
	if !strings.Contains(err.Error(), "without 'error' field") {
		t.Errorf("unexpected error: %v", err)
	}
}

// =============================================================================
// ModTimeUnix - edge cases
// =============================================================================

func TestDriveItem_ModTimeUnix_EdgeCases(t *testing.T) {
	t.Run("nil ModTime returns 0", func(t *testing.T) {
		item := &DriveItem{ModTime: nil}
		if got := item.ModTimeUnix(); got != 0 {
			t.Errorf("expected 0 for nil ModTime, got %d", got)
		}
	})

	t.Run("negative Unix time returns 0", func(t *testing.T) {
		negTime := time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC)
		item := &DriveItem{ModTime: &negTime}
		if got := item.ModTimeUnix(); got != 0 {
			t.Errorf("expected 0 for negative Unix time, got %d", got)
		}
	})

	t.Run("valid time", func(t *testing.T) {
		validTime := time.Unix(1700000000, 0)
		item := &DriveItem{ModTime: &validTime}
		if got := item.ModTimeUnix(); got != 1700000000 {
			t.Errorf("expected 1700000000, got %d", got)
		}
	})
}

// =============================================================================
// doAuthenticatedRequestWithBody - token provider error
// =============================================================================

func TestDoAuthenticatedRequest_TokenError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "", err: fmt.Errorf("token refresh failed")}

	_, err := client.GetItem(context.Background(), tokenProvider, ItemID("any"))
	if err == nil {
		t.Fatal("expected error when token provider fails")
	}
	if !strings.Contains(err.Error(), "error obtaining access token") {
		t.Errorf("unexpected error: %v", err)
	}
}

// =============================================================================
// copyItemRequest JSON omitempty behavior
// =============================================================================

func TestCopyItemRequest_Omitempty(t *testing.T) {
	req := copyItemRequest{}
	data, _ := json.Marshal(req)
	if strings.Contains(string(data), "name") {
		t.Error("name should be omitted when empty")
	}
	if strings.Contains(string(data), "parentReference") {
		t.Error("parentReference should be omitted when nil")
	}
}
