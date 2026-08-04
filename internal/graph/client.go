// Package graph provides an HTTP client for interacting with the Microsoft
// Graph API (OneDrive). Supports CRUD operations on files and folders,
// upload/download with streaming, async copy, retries with exponential
// backoff, and optimistic concurrency control via ETag/If-Match.
package graph

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the official Microsoft Graph v1.0 base URL
const DefaultBaseURL = "https://graph.microsoft.com/v1.0"

// HTTPDoer is the minimal interface that Client needs to execute HTTP requests.
// *http.Client satisfies it automatically, allowing mock injection in tests.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client is the HTTP client for interacting with the Microsoft Graph API.
// Uses HTTPDoer instead of *http.Client to allow dependency injection.
type Client struct {
	BaseURL     string
	HTTPClient  HTTPDoer
	PollBackoff time.Duration // initial backoff for WaitForAsyncOperation (default: 1s)
}

// Option is a function that configures a Client. Follows the Functional Options pattern.
type Option func(*Client)

// WithBaseURL configures the Microsoft Graph base URL.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		c.BaseURL = u
	}
}

// WithHTTPClient allows injecting a custom HTTP client (useful for tests).
func WithHTTPClient(h HTTPDoer) Option {
	return func(c *Client) {
		c.HTTPClient = h
	}
}

// WithTimeout configures the HTTP client timeout. If the current HTTPClient is
// an *http.Client (the default case), it only modifies its Timeout while
// preserving any Transport or additional configuration that has been set.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if hc, ok := c.HTTPClient.(*http.Client); ok {
			hc.Timeout = d
			return
		}
		c.HTTPClient = &http.Client{Timeout: d}
	}
}

// RetryDoer wraps an HTTPDoer with retry logic using exponential backoff.
// Automatically retries on 429 (Too Many Requests) and 503 (Service Unavailable).
// Respects the Retry-After header if present; otherwise uses exponential backoff.
type RetryDoer struct {
	inner      HTTPDoer
	maxRetries int
}

// NewRetryDoer creates a RetryDoer that retries up to maxRetries additional
// times (max 1 + maxRetries total attempts).
func NewRetryDoer(inner HTTPDoer, maxRetries int) *RetryDoer {
	return &RetryDoer{inner: inner, maxRetries: maxRetries}
}

// isNetworkError returns true if the error is transient (timeout, DNS,
// connection refused/reset) and therefore retryable. Permanent errors
// (TLS, invalid redirect) are not retried.
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	// net.Error with Timeout() == true covers timeouts and DNS
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// Temporary network errors: "connection refused", "connection reset",
	// "no such host", "network is unreachable"
	s := err.Error()
	return strings.Contains(s, "connection refused") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "network is unreachable") ||
		strings.Contains(s, "i/o timeout")
}

// Do implements HTTPDoer with retries for transient network errors and
// HTTP codes 429/503. Uses exponential backoff with Retry-After as priority.
func (r *RetryDoer) Do(req *http.Request) (*http.Response, error) {
	const baseDelay = 1 * time.Second

	for attempt := 0; ; attempt++ {
		resp, err := r.inner.Do(req)

		// Retry on transient network errors (timeout, DNS, connection refused)
		if err != nil && isNetworkError(err) && attempt < r.maxRetries {
			delay := baseDelay * time.Duration(1<<attempt)
			time.Sleep(delay)
			continue
		}
		if err != nil {
			return resp, err
		}

		// Retry on 429 (Too Many Requests) and 503 (Service Unavailable)
		if resp.StatusCode != http.StatusTooManyRequests &&
			resp.StatusCode != http.StatusServiceUnavailable {

			return resp, nil
		}

		// If we've exhausted retries, return the last response
		if attempt >= r.maxRetries {
			return resp, nil
		}

		// If the request has a body but it's not resettable (GetBody == nil),
		// we cannot safely retry: the body was already consumed on the
		// previous attempt and resending it would produce a request with an
		// empty or truncated body, with no visible error to the caller.
		// We prefer to return the current response (with its body already
		// closed below) rather than retrying blindly. Requests without a
		// body (req.Body == nil, e.g. GET) are always safe to retry.
		if req.Body != nil && req.GetBody == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return resp, nil
		}

		// Calculate delay: Retry-After takes priority, otherwise exponential backoff
		delay := baseDelay * time.Duration(1<<attempt)
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if sec, err := strconv.Atoi(ra); err == nil {
				delay = time.Duration(sec) * time.Second
			}
		}

		// Drain and close the body before retrying
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		// Reset the request body if it has one (GetBody != nil, already
		// verified above for the body case; if there was no body, there's
		// nothing to reset).
		if req.GetBody != nil {
			if rc, err := req.GetBody(); err == nil {
				req.Body = rc
			}
		}

		time.Sleep(delay)
	}
}

// WithRetry wraps the current HTTPClient in a RetryDoer with the specified
// number of retries. Should be applied after WithHTTPClient/WithTimeout.
func WithRetry(maxRetries int) Option {
	return func(c *Client) {
		c.HTTPClient = NewRetryDoer(c.HTTPClient, maxRetries)
	}
}

// NewClient creates a new client with default production configuration.
// Includes 3 retries with exponential backoff for transient network errors
// (timeout, DNS, connection refused) and HTTP codes 429/503. Use WithRetry(0)
// to disable retries.
//
//	// Production (default values):
//	client := graph.NewClient()
//
//	// Tests with httptest server:
//	client := graph.NewClient(graph.WithBaseURL(server.URL), graph.WithHTTPClient(server.Client()))
func NewClient(opts ...Option) *Client {
	c := &Client{
		BaseURL:     DefaultBaseURL,
		HTTPClient:  &http.Client{Timeout: 15 * time.Second},
		PollBackoff: 1 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	// By default, wrap in RetryDoer with 3 retries. If the caller already
	// passed WithHTTPClient or another WithRetry, we respect their configuration.
	if _, isRetry := c.HTTPClient.(*RetryDoer); !isRetry {
		c.HTTPClient = NewRetryDoer(c.HTTPClient, 3)
	}
	return c
}

// URL builds an absolute URL from a resource path and optional parameters.
// Resource identifies a OneDrive item, either by ID or by path.
// Implemented by ItemID and ItemPath.
type Resource interface {
	ResourcePath() string
	IsEmpty() bool
	ParentReference() map[string]any
}

// ItemID identifies a OneDrive resource by its unique ID.
// Example: graph.ItemID("01BYE5RZ6QN3VXWN...")
type ItemID string

// RootID is the special ID that references the drive's root folder.
const RootID ItemID = "root"

// ItemPath identifies a OneDrive resource by its path within the drive.
// Example: graph.ItemPath("/Documents/photo.jpg")
type ItemPath string

// ResourcePath returns the Graph resource path for this ItemID.
func (id ItemID) ResourcePath() string { return ResourcePathByID(string(id)) }

// IsEmpty returns true if ItemID is an empty string.
func (id ItemID) IsEmpty() bool { return string(id) == "" }

// ResourcePath returns the Graph resource path for this ItemPath.
func (p ItemPath) ResourcePath() string { return ResourcePathByPath(string(p)) }

// IsEmpty returns true if ItemPath is an empty string.
func (p ItemPath) IsEmpty() bool { return string(p) == "" }

// ParentReference returns a map {"id": "..."} for use in MoveItem/CopyItem.
func (id ItemID) ParentReference() map[string]any {
	return map[string]any{"id": string(id)}
}

// ParentReference returns a map {"path": "/..."} for use in MoveItem/CopyItem.
func (p ItemPath) ParentReference() map[string]any {
	return map[string]any{"path": `/` + normalizePath(string(p))}
}

// URL builds an absolute Microsoft Graph URL from a resource path
// and optional query parameters.
func (cli *Client) URL(resourcePath string, query url.Values) string {
	base := strings.TrimRight(cli.BaseURL, "/")
	if !strings.HasPrefix(resourcePath, "/") {
		resourcePath = "/" + resourcePath
	}
	u := base + resourcePath
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

// ResourcePathByID returns the resource path of an item addressed by ID.
//
//	"root"      -> /me/drive/root
//	"01ABC..."  -> /me/drive/items/01ABC...
func ResourcePathByID(id string) string {
	if id == "root" {
		return "/me/drive/root"
	}
	return "/me/drive/items/" + url.PathEscape(id)
}

// ResourcePathByPath returns the resource path of an item addressed by path.
//
//	"/"                      -> /me/drive/root
//	"/Documents/photo.jpg"   -> /me/drive/root:/Documents/photo.jpg
func ResourcePathByPath(p string) string {
	clean := normalizePath(p)
	if clean == "" {
		return "/me/drive/root"
	}
	return "/me/drive/root:/" + escapePathSegments(clean)
}

// normalizePath cleans a filesystem path and returns a path
// relative to the drive root, without leading or trailing "/".
// "/" , "" , "." , "/foo/../" -> ""
func normalizePath(p string) string {
	// Remove null bytes (path.Clean doesn't handle them)
	p = strings.ReplaceAll(p, "\x00", "")
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean("/" + p) // resolves "..", ".", "//" and guarantees leading "/"
	return strings.Trim(p, "/")
}

// escapePathSegments escapes each path segment preserving the "/".
// Note: url.PathEscape on the full path would also escape the slashes.
func escapePathSegments(p string) string {
	segments := strings.Split(p, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

// WithAction adds a navigation/action ("children", "content", "delta"...)
// to a resource path, respecting the path-based addressing syntax.
//
//	/me/drive/root                    + children -> /me/drive/root/children
//	/me/drive/items/123               + children -> /me/drive/items/123/children
//	/me/drive/root:/Documents         + children -> /me/drive/root:/Documents:/children
func WithAction(resource, action string) string {
	action = strings.Trim(action, "/")
	if action == "" {
		return resource
	}
	// If the path contains ":" it's path-based addressing and must be closed.
	if strings.Contains(resource, ":") {
		return resource + ":/" + action
	}
	return resource + "/" + action
}

// ChildrenPathByID returns the resource path for listing children of an item by ID.
func ChildrenPathByID(id string) string { return WithAction(ResourcePathByID(id), "children") }

// ChildrenPathByPath returns the resource path for listing children of an item by path.
func ChildrenPathByPath(p string) string { return WithAction(ResourcePathByPath(p), "children") }

// ContentPathByID returns the resource path for downloading content of an item by ID.
func ContentPathByID(id string) string { return WithAction(ResourcePathByID(id), "content") }

// ContentPathByPath returns the resource path for downloading content of an item by path.
func ContentPathByPath(p string) string { return WithAction(ResourcePathByPath(p), "content") }

// DeltaPath returns the resource path for the delta endpoint of the drive root.
func DeltaPath() string { return WithAction(ResourcePathByID("root"), "delta") }
