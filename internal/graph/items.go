package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/frosado/onecloudriver/internal/types"
)

// GetItem retrieves a DriveItem by its ID.
//
// The itemID parameter can be:
//   - The ID of any DriveItem (e.g.: "01BYE5RZ6QN3VXWN...")
//   - The string "root" to reference the root item
//
// Example:
//
//	item, err := client.GetItem(ctx, account, "01BYE5RZ6QN3VXWN...")
//	if err != nil {
//	    return err
//	}
//	fmt.Println("Name:", item.Name)
func (cli *Client) GetItem(ctx context.Context, tokenProvider types.TokenProvider, res Resource) (*DriveItem, error) {
	if err := validateResource(res); err != nil {
		return nil, err
	}
	rURL := cli.URL(res.ResourcePath(), nil)
	return doJSONRequest[DriveItem](
		ctx,
		cli,
		http.MethodGet,
		rURL,
		nil,
		nil,
		tokenProvider,
	)
}

// CreateFolder creates a new folder inside the specified parent directory.
//
// The parent parameter can be an ItemID or ItemPath pointing to the container folder.
//
// Example:
//
//	folder, err := client.CreateFolder(ctx, account, graph.ItemID("folder123"), "New Folder")
//	if err != nil {
//	    return err
//	}
//	fmt.Println("Created:", folder.Name)
func (cli *Client) CreateFolder(ctx context.Context, tokenProvider types.TokenProvider, parent Resource, name string) (*DriveItem, error) {
	if err := validateResource(parent); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, ErrEmptyName
	}
	rURL := cli.URL(WithAction(parent.ResourcePath(), "children"), nil)

	req := &createFolderRequest{Name: name, Folder: struct{}{}}
	return doJSONRequest[DriveItem](
		ctx,
		cli,
		http.MethodPost,
		rURL,
		req,
		nil,
		tokenProvider,
	)
}

// DeleteItem deletes an item (file or folder) from OneDrive.
//
// Accepts a Resource (ItemID or ItemPath) to identify the item to delete.
// The etag parameter enables optimistic concurrency control: if not empty,
// it is sent as an If-Match header to avoid deleting an outdated version.
// Returns nil if the operation was successful.
//
// Example:
//
//	err := client.DeleteItem(ctx, account, graph.ItemID("file123"), "")
//	if err != nil {
//	    return err
//	}
func (cli *Client) DeleteItem(ctx context.Context, tokenProvider types.TokenProvider, r Resource, etag string) error {
	if err := validateResource(r); err != nil {
		return err
	}

	rURL := cli.URL(r.ResourcePath(), nil)

	var hdrs map[string]string
	if etag != "" {
		hdrs = map[string]string{"If-Match": etag}
	}

	resp, err := cli.doAuthenticatedRequestWithBody(
		ctx,
		http.MethodDelete,
		rURL,
		nil,
		"",
		hdrs,
		tokenProvider,
	)

	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return checkResponse(resp)
}

// RenameItem renames an item (file or folder) in OneDrive.
//
// Accepts a Resource (ItemID or ItemPath) to identify the item to rename.
// The etag parameter enables optimistic concurrency control: if not empty,
// it is sent as an If-Match header to avoid renaming an outdated version.
// Returns the DriveItem updated with the new name.
//
// Example:
//
//	item, err := client.RenameItem(ctx, account, graph.ItemID("file123"), "new-name.pdf", item.ETag)
//	if err != nil {
//	    return err
//	}
//	fmt.Println("Renamed to:", item.Name)
func (cli *Client) RenameItem(ctx context.Context, tokenProvider types.TokenProvider, r Resource, newName string, etag string) (*DriveItem, error) {
	if err := validateResource(r); err != nil {
		return nil, err
	}
	if newName == "" {
		return nil, ErrEmptyName
	}

	var hdrs map[string]string
	if etag != "" {
		hdrs = map[string]string{"If-Match": etag}
	}

	rURL := cli.URL(r.ResourcePath(), nil)
	return doJSONRequest[DriveItem](
		ctx,
		cli,
		http.MethodPatch,
		rURL,
		&renameItemRequest{Name: newName},
		hdrs,
		tokenProvider,
	)
}

// MoveItem moves an item (file or folder) to a new parent folder in OneDrive.
//
// Accepts:
//   - item: the Resource (ItemID or ItemPath) of the item to move
//   - newParent: the Resource (ItemID or ItemPath) of the destination folder
//   - etag: optimistic concurrency control (empty = no control)
//
// If newParent is an ItemID, the body includes {"id": "..."}.
// If newParent is an ItemPath, the body includes {"path": "..."}.
//
// Example:
//
//	item, err := client.MoveItem(ctx, account, graph.ItemID("file123"), graph.ItemID("folder456"), item.ETag)
//	if err != nil {
//	    return err
//	}
//	fmt.Println("Moved to:", item.Parent.ID)
func (cli *Client) MoveItem(ctx context.Context, tokenProvider types.TokenProvider, item Resource, newParent Resource, etag string) (*DriveItem, error) {
	if err := validateResource(item); err != nil {
		return nil, err
	}
	if err := validateResource(newParent); err != nil {
		return nil, err
	}

	var hdrs map[string]string
	if etag != "" {
		hdrs = map[string]string{"If-Match": etag}
	}

	originURL := cli.URL(item.ResourcePath(), nil)

	return doJSONRequest[DriveItem](
		ctx,
		cli,
		http.MethodPatch,
		originURL,
		&moveItemRequest{ParentReference: newParent.ParentReference()},
		hdrs,
		tokenProvider,
	)
}

// CopyItem copies an item (file or folder) to a new location and/or name in OneDrive.
//
// The copy operation is asynchronous in Microsoft Graph.
// Returns the monitoring URL (from the Location header) for polling progress.
//
// Parameters:
//   - item: Resource of the item to copy
//   - newName: new name for the copy (empty = keep the original)
//   - newParent: Resource of the destination folder (empty = same folder)
//
// At least one of newName or newParent must be specified.
//
// Example:
//
//	monitorURL, err := client.CopyItem(ctx, account, graph.ItemID("file123"), "copy.pdf", graph.ItemID("folder456"))
//	if err != nil {
//	    return err
//	}
//	fmt.Println("Monitoring at:", monitorURL)
func (cli *Client) CopyItem(ctx context.Context, tokenProvider types.TokenProvider, item Resource, newName string, newParent Resource) (string, error) {
	if err := validateResource(item); err != nil {
		return "", err
	}
	if newName == "" && (newParent == nil || newParent.IsEmpty()) {
		return "", fmt.Errorf("at least newName or newParent must be specified")
	}

	// Build parentReference only if a new parent was specified.
	// newParent can be nil when only the name is being changed.
	var parentRef map[string]any
	if newParent != nil && !newParent.IsEmpty() {
		parentRef = newParent.ParentReference()
	}

	copyReq := &copyItemRequest{
		Name:            newName,
		ParentReference: parentRef,
	}

	jsonBody, err := json.Marshal(copyReq)
	if err != nil {
		return "", fmt.Errorf("error serializing body: %w", err)
	}

	rURL := cli.URL(WithAction(item.ResourcePath(), "copy"), nil)
	resp, err := cli.doAuthenticatedRequestWithBody(ctx, http.MethodPost, rURL, bytes.NewReader(jsonBody), "application/json", nil, tokenProvider)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return "", err
	}

	// The copy API returns 202 Accepted with the monitoring URL in the Location header.
	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("unexpected server response: HTTP %d", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("202 response without Location header")
	}

	return loc, nil
}

// AsyncOperationStatus represents the status of an asynchronous Graph operation
type AsyncOperationStatus struct {
	Status   string      `json:"status"` // "inProgress", "completed", "failed"
	Resource *DriveItem  `json:"resource,omitempty"`
	Error    *GraphError `json:"error,omitempty"`
}

// WaitForAsyncOperation polls the monitoring URL until the operation completes.
// Uses exponential backoff (1s, 2s, 4s...) and respects context cancellation.
func (cli *Client) WaitForAsyncOperation(ctx context.Context, monitorURL string) (*DriveItem, error) {
	if monitorURL == "" {
		return nil, fmt.Errorf("the monitoring URL cannot be empty")
	}

	backoff := cli.PollBackoff
	maxBackoff := 10 * cli.PollBackoff

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err() // The user cancelled (e.g.: Ctrl+C or unmounted FUSE)
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, monitorURL, nil)
		if err != nil {
			return nil, fmt.Errorf("error creating monitoring request: %w", err)
		}

		// Note: Graph monitoring URLs sometimes don't require the Bearer token,
		// but if your implementation requires it, add it here.
		resp, err := cli.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("network error while monitoring: %w", err)
		}

		var status AsyncOperationStatus
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("error parsing operation status: %w", err)
		}
		resp.Body.Close()

		switch status.Status {
		case "completed":
			if status.Resource == nil {
				return nil, fmt.Errorf("operation completed but without returned resource")
			}
			return status.Resource, nil
		case "failed":
			if status.Error != nil {
				return nil, status.Error
			}
			return nil, fmt.Errorf("the asynchronous operation failed without detail")
		case "inProgress":
			// Wait with exponential backoff before the next attempt
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
			}
		default:
			return nil, fmt.Errorf("unknown operation status: %s", status.Status)
		}
	}
}
