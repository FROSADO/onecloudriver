package graph

import (
	"context"
	"net/url"
	"strings"

	"github.com/frosado/onecloudriver/internal/types"
	"github.com/rs/zerolog/log"
)

// ListDriveRoot retrieves the items at the root of the user's OneDrive.
//
// Automatically handles pagination by following @odata.nextLink links
// until all available items are obtained.
//
// The tokenProvider is used to obtain the access token on each request,
// allowing automatic refresh if the token expires during the operation.
func (cli *Client) ListDriveRoot(ctx context.Context, tokenProvider types.TokenProvider) ([]DriveItem, error) {
	rURL := cli.URL(
		WithAction(ResourcePathByID("root"), "children"),
		url.Values{"$top": {"200"}},
	)
	return cli.listDriveItems(ctx, tokenProvider, rURL)
}

// ListChildren retrieves the child items of a specific folder by its ID.
//
// The itemID parameter can be:
//   - A DriveItem ID (e.g.: "01BYE5RZ6QN3VXWN...")
//   - The string "root" to reference the root folder
//
// Automatically handles pagination.
//
// Example:
//
//	// List the contents of a folder
//	children, err := client.ListChildren(ctx, account, folderItem.ID)
//	if err != nil {
//	    return err
//	}
//	for _, child := range children {
//	    fmt.Println(child.Name)
//	}
func (cli *Client) ListChildren(ctx context.Context, tokenProvider types.TokenProvider, r Resource) ([]DriveItem, error) {
	if err := validateResource(r); err != nil {
		return nil, err
	}

	rURL := cli.URL(WithAction(r.ResourcePath(), "children"), url.Values{"$top": {"200"}})
	return cli.listDriveItems(ctx, tokenProvider, rURL)
}

// listDriveItems is the private method that implements the common logic for
// retrieving a list of DriveItems from any Graph endpoint.
//
// Handles:
//   - Token authentication (with automatic refresh)
//   - Automatic pagination following @odata.nextLink
//   - Resolution of relative URLs in @odata.nextLink (Graph sometimes returns them without host)
//   - JSON response parsing
//   - Result accumulation
func (cli *Client) listDriveItems(ctx context.Context, tokenProvider types.TokenProvider, initialURL string) ([]DriveItem, error) {
	var allItems []DriveItem
	nextURL := initialURL
	pageCount := 0

	for nextURL != "" {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		pageCount++

		// Get a page of items
		var page driveItemPage
		var err error
		page, nextURL, err = cli.fetchDriveItemPage(ctx, tokenProvider, nextURL)
		if err != nil {
			return nil, err
		}

		// Accumulate items from this page
		allItems = append(allItems, page.Value...)

		log.Debug().
			Int("page", pageCount).
			Int("itemsInPage", len(page.Value)).
			Int("totalSoFar", len(allItems)).
			Bool("hasNextPage", nextURL != "").
			Msg("Graph pagination page fetched")

		// Resolver URLs relativas en @odata.nextLink (Graph a veces
		// returns /me/drive/... without the host)
		if nextURL != "" && !strings.HasPrefix(nextURL, "http") {
			base := strings.TrimRight(cli.BaseURL, "/")
			if !strings.HasPrefix(nextURL, "/") {
				nextURL = "/" + nextURL
			}
			nextURL = base + nextURL
			log.Debug().Str("resolved", nextURL).Msg("Resolved relative @odata.nextLink")
		}
	}

	log.Debug().Int("totalItems", len(allItems)).Int("totalPages", pageCount).Msg("Graph pagination complete")

	return allItems, nil
}
