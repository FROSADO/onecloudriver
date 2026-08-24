package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/frosado/onecloudriver/internal/types"
)

// DeletedState indicates that an item was deleted on the server.
// Used in DeltaItem to distinguish deletions from other operations.
type DeletedState struct {
	State string `json:"state,omitempty"`
}

// DeltaItem is an item returned by the Microsoft Graph delta endpoint.
// Adds the Deleted field (absent in normal DriveItem) to detect deletions.
type DeltaItem struct {
	DriveItem
	Deleted *DeletedState `json:"deleted,omitempty"`
}

// deltaResponse is the pagination structure of the delta endpoint.
type deltaResponse struct {
	NextLink  string      `json:"@odata.nextLink,omitempty"`
	DeltaLink string      `json:"@odata.deltaLink,omitempty"`
	Values    []DeltaItem `json:"value,omitempty"`
}

// PollDelta queries the OneDrive delta endpoint. If link is "", it starts
// from the beginning (no token). Handles pagination: if the response includes
// @odata.nextLink, it returns the items and cont=true so the caller keeps
// paging. If it includes @odata.deltaLink, it's the last page: cont=false and
// nextLink contains the delta link for the next polling cycle.
//
// Example usage with pagination:
//
//	link := ""
//	for {
//	    items, nextLink, cont, err := client.PollDelta(ctx, tp, link)
//	    if err != nil { break }	//    // process items...
//	    link = nextLink
//	    if !cont { break }
//	}
func (cli *Client) PollDelta(ctx context.Context, tokenProvider types.TokenProvider, link string) ([]DeltaItem, string, bool, error) {
	var reqURL string
	if link == "" {
		reqURL = cli.URL(DeltaPath(), nil)
	} else {
		reqURL = cli.absoluteURL(link)
		// The link comes from a previous server response, and the request
		// below carries the bearer token: only follow it if it stays on
		// the configured Graph endpoint.
		if err := cli.validateFollowURL(reqURL); err != nil {
			return nil, "", false, err
		}
	}

	resp, err := cli.doAuthenticatedRequestWithBody(ctx, http.MethodGet, reqURL, nil, "", nil, tokenProvider)
	if err != nil {
		return nil, "", false, fmt.Errorf("error calling delta: %w", err)
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, "", false, fmt.Errorf("error in delta response: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", false, fmt.Errorf("error reading delta body: %w", err)
	}

	var page deltaResponse
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, "", false, fmt.Errorf("error parsing delta response: %w", err)
	}

	// nextLink → continuar paginando
	if page.NextLink != "" {
		return page.Values, page.NextLink, true, nil
	}

	// deltaLink → last page of the cycle, return the link verbatim (absolute
	// or bare path). The caller passes it back on the next poll, where
	// absoluteURL + validateFollowURL resolve and check it before the request
	// is sent, so no normalization is needed here.
	return page.Values, page.DeltaLink, false, nil
}
