package graph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateFollowURL(t *testing.T) {
	cli := &Client{BaseURL: "https://graph.microsoft.com/v1.0"}

	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{"same host", "https://graph.microsoft.com/v1.0/me/drive/root/delta?token=abc", false},
		{"case-insensitive host", "https://GRAPH.microsoft.com/v1.0/me/drive/root", false},
		{"foreign host", "https://evil.example.com/v1.0/me/drive/root", true},
		{"host suffix trick", "https://graph.microsoft.com.evil.example.com/v1.0", true},
		{"downgraded scheme", "http://graph.microsoft.com/v1.0/me/drive/root", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := cli.validateFollowURL(tc.rawURL)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for %q", tc.rawURL)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for %q: %v", tc.rawURL, err)
			}
		})
	}
}

// TestListChildren_RejectsForeignNextLink ensures a nextLink pointing at
// another host is not followed, since the request would carry the bearer
// token.
func TestListChildren_RejectsForeignNextLink(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[{"id":"item1","name":"a.txt","size":1}],
			"@odata.nextLink":"https://evil.example.com/next"}`))
	}))
	defer server.Close()

	cli := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	_, err := cli.ListChildren(context.Background(), &testTokenProvider{"token"}, ItemID("root"))
	if err == nil {
		t.Fatal("expected error for foreign @odata.nextLink")
	}
	if !strings.Contains(err.Error(), "does not match the Graph endpoint") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPollDelta_RejectsForeignLink(t *testing.T) {
	cli := &Client{BaseURL: "https://graph.microsoft.com/v1.0", HTTPClient: http.DefaultClient}

	_, _, _, err := cli.PollDelta(context.Background(), &testTokenProvider{"token"},
		"https://evil.example.com/me/drive/root/delta?token=abc")
	if err == nil {
		t.Fatal("expected error for foreign delta link")
	}
	if !strings.Contains(err.Error(), "does not match the Graph endpoint") {
		t.Errorf("unexpected error: %v", err)
	}
}
