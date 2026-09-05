package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"
)

// defaultTimeout bounds each control request. A local socket answers in
// microseconds; the timeout only guards against a hung server.
const defaultTimeout = 5 * time.Second

// Client is a client for the control server Unix socket API.
type Client struct {
	sockPath string
	httpCli  *http.Client
}

// Errors returned by Client operations.
var (
	// ErrNotRunning indicates that no control server is listening on the
	// socket (it does not exist, or a stale socket is present).
	ErrNotRunning = errors.New("control server not running (socket not found)")
	// ErrNotFound indicates the endpoint returned 404.
	ErrNotFound = errors.New("endpoint not found (404)")
	// ErrMethodNotAllowed indicates the endpoint returned 405.
	ErrMethodNotAllowed = errors.New("method not allowed (405)")
)

// NewClient creates a client that connects to the control server at the given
// Unix socket path.
func NewClient(sockPath string) *Client {
	dialer := &net.Dialer{Timeout: defaultTimeout}
	return &Client{
		sockPath: sockPath,
		httpCli: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", sockPath)
				},
			},
			Timeout: defaultTimeout,
		},
	}
}

// Info fetches the /v1/info endpoint from the control server.
func (c *Client) Info(ctx context.Context) (*Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/v1/info", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpCli.Do(req)
	if err != nil {
		if isNotRunningError(err) {
			return nil, ErrNotRunning
		}
		return nil, fmt.Errorf("requesting info: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusMethodNotAllowed:
		return nil, ErrMethodNotAllowed
	default:
		return nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &info, nil
}

// isNotRunningError reports whether err means that no server is listening on
// the Unix socket. The HTTP transport wraps the dial failure; unwrap it to the
// underlying syscall error instead of matching on error strings.
func isNotRunningError(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return false
	}
	return isSocketUnreachable(opErr)
}

// isSocketUnreachable reports whether a net.OpError stems from a missing or
// non-listening Unix socket (ENOENT / ECONNREFUSED / ENOTSOCK).
func isSocketUnreachable(opErr *net.OpError) bool {
	if err := unwrapToSyscallErrno(opErr); err != nil {
		return errors.Is(err, syscall.ENOENT) ||
			errors.Is(err, syscall.ECONNREFUSED) ||
			errors.Is(err, syscall.ENOTSOCK)
	}
	return false
}

// unwrapToSyscallErrno walks the OpError chain to the first syscall.Errno.
func unwrapToSyscallErrno(err error) error {
	for err != nil {
		var syscallErr syscall.Errno
		if errors.As(err, &syscallErr) {
			return syscallErr
		}
		err = errors.Unwrap(err)
	}
	return nil
}
