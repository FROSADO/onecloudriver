package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/frosado/onecloudriver/internal/types"
)

// validateResource checks that a Resource is not nil or empty.
func validateResource(r Resource) error {
	if r == nil || r.IsEmpty() {
		return ErrEmptyResource
	}
	return nil
}

// doAuthenticatedRequest creates and executes an authenticated HTTP request to Graph API.
//
// This private method centralizes:
//   - Request creation with context
//   - Token retrieval (with automatic refresh)
//   - Header configuration (Authorization, Accept)
//   - Request execution
//
// The caller is responsible for closing resp.Body with defer.
func (cli *Client) doAuthenticatedRequest(ctx context.Context, method, reqURL string, tokenProvider types.TokenProvider) (*http.Response, error) {
	return cli.doAuthenticatedRequestWithBody(ctx, method, reqURL, nil, "", nil, tokenProvider)
}

// doAuthenticatedRequestWithBody creates and executes an authenticated HTTP request with an optional body.
//
// Centralizes:
//   - Request creation with context and body
//   - Token retrieval (with automatic refresh)
//   - Header configuration (Authorization, Accept, Content-Type)
//   - Request execution
//
// The caller is responsible for closing resp.Body with defer.
func (cli *Client) doAuthenticatedRequestWithBody(ctx context.Context, method, reqURL string, body io.Reader, contentType string, extraHeaders map[string]string, tokenProvider types.TokenProvider) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	token, err := tokenProvider.GetAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("error obtaining access token: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := cli.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error querying Graph: %w", err)
	}

	return resp, nil
}

// doJSONRequest executes an HTTP request to Graph API that returns JSON.
//
// Centralizes body serialization, authentication, response validation,
// and decoding of the JSON response into a generic type T.
//
// Parameters:
//   - method: HTTP method (GET, POST, PATCH, DELETE)
//   - url: absolute endpoint URL
//   - reqBody: request body (nil = no body). Serialized to JSON.
//   - extraHeaders: additional headers (nil = no extra headers). Useful for If-Match, etc.
//   - tokenProvider: access token source
func doJSONRequest[T any](ctx context.Context, c *Client, method, url string,
	reqBody any, extraHeaders map[string]string, tokenProvider types.TokenProvider) (*T, error) {

	var body io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("error serializing body: %w", err)
		}
		body = bytes.NewReader(data)
	}

	resp, err := c.doAuthenticatedRequestWithBody(ctx, method, url, body,
		"application/json", extraHeaders, tokenProvider)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error parsing Graph response: %w", err)
	}
	return &result, nil
}

// fetchDriveItemPage fetches a single page of DriveItems from Graph API.
//
// Returns:
//   - The parsed page of items
//   - The URL of the next page (empty string if no more pages)
//   - Error if something fails
func (cli *Client) fetchDriveItemPage(ctx context.Context, tokenProvider types.TokenProvider, reqURL string) (driveItemPage, string, error) {
	page, err := doJSONRequest[driveItemPage](ctx, cli, http.MethodGet, reqURL, nil, nil, tokenProvider)
	if err != nil {
		return driveItemPage{}, "", err
	}
	return *page, page.NextLink, nil
}
