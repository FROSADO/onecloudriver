package graph

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

var (
	ErrItemNotFound  = errors.New("Item not found")
	ErrInvalidToken  = errors.New("Invalid token")
	ErrEmptyName     = errors.New("The name cannot be empty")
	ErrEmptyResource = errors.New("The resource cannot be empty")
	ErrNilContent    = errors.New("The content cannot be nil")
	ErrInvalidName   = errors.New("Invalid item name")
	ErrThrottled     = errors.New("Too many requests to the API") // new (429)
	ErrConflict      = errors.New("Conflict while modifying")     // new (409)
)

// GraphError represents an error returned by the Microsoft Graph API.
type GraphError struct {
	StatusCode int    // HTTP code (e.g.: 404, 401)
	Code       string `json:"code"`    // Graph error code (e.g.: "itemNotFound")
	Message    string `json:"message"` // Descriptive error message
}

func (e *GraphError) Error() string {
	return fmt.Sprintf("graph error HTTP %d: [%s] %s", e.StatusCode, e.Code, e.Message)
}

// Is allows using errors.Is to check sentinel errors by HTTP code.
func (e *GraphError) Is(target error) bool {
	if target == ErrItemNotFound {
		return e.StatusCode == http.StatusNotFound
	}
	if target == ErrInvalidToken {
		return e.StatusCode == http.StatusUnauthorized
	}
	if target == ErrThrottled {
		return e.StatusCode == http.StatusTooManyRequests
	}
	if target == ErrConflict {
		return e.StatusCode == http.StatusConflict
	}
	return false
}

// checkResponse validates the HTTP status and parses Microsoft Graph errors.
//
// Behavior:
//   - If the status is 2xx (success): returns nil WITHOUT consuming the body
//   - If the status is an error (4xx, 5xx): CONSUMES the body to parse the Graph error
//
// Important: The caller must always use defer resp.Body.Close().
// If checkResponse returns an error, the body was ALREADY consumed (but must still be closed with defer).
// If checkResponse returns nil, the body is INTACT and ready to be read.
func checkResponse(resp *http.Response) error {
	// Success case: do NOT touch the body so the caller can read it
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// Error case: CONSUME the body to get the error details
	// Read with a limit for security (avoid huge malicious responses)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<10)) // Max 10KB
	if err != nil {
		return &GraphError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("error reading response body: %v", err),
		}
	}

	// Try to parse the Graph error JSON
	var errResp struct {
		Error *GraphError `json:"error"`
	}

	if err := json.Unmarshal(body, &errResp); err != nil {
		// If not valid JSON, return the raw body (truncated if too long)
		bodyStr := string(body)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return &GraphError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("non-JSON response: %s", bodyStr),
		}
	}

	if errResp.Error != nil {
		errResp.Error.StatusCode = resp.StatusCode
		return errResp.Error
	}

	// Valid JSON response but without the "error" field
	return &GraphError{
		StatusCode: resp.StatusCode,
		Message:    "unknown Graph error (JSON without 'error' field)",
	}
}
