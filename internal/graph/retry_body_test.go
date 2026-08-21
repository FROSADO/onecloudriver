package graph

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A retry whose request body cannot be rewound must fail instead of resending
// the consumed body: the server would otherwise receive a truncated request
// and its answer would be reported as the real outcome.
func TestRetryDoer_BodyResetFailureAbortsRetry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusServiceUnavailable) // retryable
	}))
	defer server.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		server.URL+"/upload", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	getBodyErr := errors.New("snapshot no longer readable")
	req.GetBody = func() (io.ReadCloser, error) { return nil, getBodyErr }

	resp, err := NewRetryDoer(server.Client(), 2).Do(req)
	if resp != nil {
		resp.Body.Close()
		t.Fatal("expected no response when the body cannot be reset")
	}
	if !errors.Is(err, getBodyErr) {
		t.Fatalf("got %v, want the GetBody failure", err)
	}
	if attempts != 1 {
		t.Errorf("the request must not be retried with a consumed body, attempts: %d", attempts)
	}
}

// A rewindable body is still retried normally.
func TestRetryDoer_RetriesWithRewoundBody(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(data))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		server.URL+"/upload", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	resp, err := NewRetryDoer(server.Client(), 2).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
	if len(bodies) != 2 || bodies[0] != "payload" || bodies[1] != "payload" {
		t.Errorf("the retry must resend the full body, got %q", bodies)
	}
}
