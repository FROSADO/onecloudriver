package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"github.com/frosado/onecloudriver/internal/types"
)

// UploadItem uploads a file to OneDrive via a simple PUT request.
//
// Supports files up to 4 MB. For larger files, use UploadItemStream.
//
// Parameters:
//   - parent: Resource (ItemID or ItemPath) of the destination folder
//   - fileName: name of the file to create in OneDrive
//   - content: io.Reader with the file content
//   - etag: optimistic concurrency control (empty = no control). When not
//     empty, it is sent as an If-Match header so the upload only overwrites
//     the server item if it still matches this ETag; otherwise the API
//     returns 412 Precondition Failed (see ErrPreconditionFailed).
//
// Example:
//
//	file, _ := os.Open("photo.jpg")
//	defer file.Close()
//	item, err := client.UploadItem(ctx, account, graph.ItemID("folder123"), "photo.jpg", file, "")
//	if err != nil {
//	    return err
//	}
//	fmt.Println("Uploaded:", item.Name)
func (cli *Client) UploadItem(ctx context.Context, tokenProvider types.TokenProvider, parent Resource, fileName string, content io.Reader, etag string) (*DriveItem, error) {
	if err := validateResource(parent); err != nil {
		return nil, err
	}
	if fileName == "" {
		return nil, fmt.Errorf("file name cannot be empty")
	}
	if content == nil {
		return nil, fmt.Errorf("content cannot be nil")
	}

	var hdrs map[string]string
	if etag != "" {
		hdrs = map[string]string{"If-Match": etag}
	}

	resourcePath := parent.ResourcePath() + ":/" + url.PathEscape(fileName)
	reqURL := cli.URL(WithAction(resourcePath, "content"), nil)

	resp, err := cli.doAuthenticatedRequestWithBody(ctx, http.MethodPut, reqURL, content, "application/octet-stream", hdrs, tokenProvider)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	var item DriveItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, fmt.Errorf("error parsing Graph response: %w", err)
	}

	return &item, nil
}

// chunkPool is a sync.Pool that reuses chunkSize-byte buffers between
// UploadItemStream calls, reducing garbage collector pressure during
// large file uploads.
var chunkPool = sync.Pool{
	New: func() any {
		b := make([]byte, chunkSize)
		return &b
	},
}

// chunkSize is the chunk size used by UploadItemStream: 320 KiB.
// This is the minimum required by Microsoft Graph for upload sessions.
const chunkSize int64 = 327680

// UploadItemStream uploads a large file to OneDrive using upload sessions.
//
// Creates an upload session and uploads the file in 320 KiB chunks (minimum required).
// Unlike UploadItem, it requires knowing the total file size.
//
// Parameters:
//   - content: io.Reader with the file content
//   - fileSize: total file size in bytes
//   - etag: optimistic concurrency control (empty = no control). When not
//     empty, it is sent as an If-Match header on the createUploadSession
//     request so the upload only replaces the server item if it still
//     matches this ETag; otherwise the API returns 412 Precondition Failed.
//
// Example:
//
//	file, _ := os.Open("large_file.zip")
//	defer file.Close()
//	stat, _ := file.Stat()
//	item, err := client.UploadItemStream(ctx, account, graph.ItemID("folder123"), "large.zip", file, stat.Size(), "")
func (cli *Client) UploadItemStream(ctx context.Context, tokenProvider types.TokenProvider, parent Resource, fileName string, content io.Reader, fileSize int64, etag string) (*DriveItem, error) {
	if err := validateResource(parent); err != nil {
		return nil, err
	}
	if fileName == "" {
		return nil, fmt.Errorf("file name cannot be empty")
	}
	if content == nil {
		return nil, fmt.Errorf("content cannot be nil")
	}
	if fileSize <= 0 {
		return nil, fmt.Errorf("file size must be positive")
	}

	// 1. Create upload session
	resourcePath := parent.ResourcePath() + ":/" + url.PathEscape(fileName)
	sessionURL := cli.URL(WithAction(resourcePath, "createUploadSession"), nil)

	var hdrs map[string]string
	if etag != "" {
		hdrs = map[string]string{"If-Match": etag}
	}

	session, err := doJSONRequest[createUploadSessionResponse](ctx, cli, http.MethodPost, sessionURL,
		&createUploadSessionRequest{
			Item: struct {
				ConflictBehavior string `json:"@microsoft.graph.conflictBehavior"`
			}{ConflictBehavior: "rename"},
		}, hdrs, tokenProvider)
	if err != nil {
		return nil, err
	}
	if session.UploadURL == "" {
		return nil, fmt.Errorf("response does not contain uploadUrl")
	}

	// Clean up the upload session if the upload does not complete (error, cancellation, etc.).
	// Runs in a goroutine with context.Background() because the original context
	// may already be cancelled when the defer runs.
	var uploadCommitted bool
	defer func() {
		if !uploadCommitted {
			go cli.cancelUploadSession(session.UploadURL)
		}
	}()

	// 2. Upload chunks (minimum 320 KiB, except the last one)
	var item *DriveItem

	// Get buffer from pool and return it when done.
	bufPtr := chunkPool.Get().(*[]byte) //nolint:errcheck // chunkPool.New always returns *[]byte
	defer chunkPool.Put(bufPtr)
	buf := *bufPtr

	for start := int64(0); start < fileSize; start += chunkSize {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		end := start + chunkSize - 1
		if end >= fileSize-1 {
			end = fileSize - 1
		}

		chunkLen := end - start + 1
		if _, err := io.ReadFull(content, buf[:chunkLen]); err != nil {
			return nil, fmt.Errorf("error reading chunk %d-%d: %w", start, end, err)
		}

		// Clone the chunk so the HTTP transport doesn't race with the
		// next iteration of the loop over buf.
		chunkData := bytes.Clone(buf[:chunkLen])
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, session.UploadURL, bytes.NewReader(chunkData))
		if err != nil {
			return nil, fmt.Errorf("error creating chunk request: %w", err)
		}
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize))
		req.ContentLength = chunkLen

		chunkResp, err := cli.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("network error uploading chunk %d-%d: %w", start, end, err)
		}

		switch chunkResp.StatusCode {
		case http.StatusCreated, http.StatusOK:
			item = &DriveItem{}
			if err := json.NewDecoder(chunkResp.Body).Decode(item); err != nil {
				chunkResp.Body.Close()
				return nil, fmt.Errorf("error parsing uploaded item: %w", err)
			}
			chunkResp.Body.Close()
			uploadCommitted = true
		case http.StatusAccepted:
			chunkResp.Body.Close()
			continue
		default:
			bodyBytes, readErr := io.ReadAll(chunkResp.Body)
			_ = chunkResp.Body.Close()
			if readErr != nil {
				return nil, fmt.Errorf("error uploading chunk %d-%d: HTTP %d (error reading response: %w)", start, end, chunkResp.StatusCode, readErr)
			}
			return nil, fmt.Errorf("error uploading chunk %d-%d: HTTP %d: %s", start, end, chunkResp.StatusCode, string(bodyBytes))
		}

		break // Last chunk processed
	}

	if item == nil {
		return nil, fmt.Errorf("DriveItem not received in the last chunk")
	}

	return item, nil
}

// cancelUploadSession sends a DELETE request to the uploadURL to cancel
// and release an upload session that was not completed. Runs in background
// (via goroutine in UploadItemStream) because the original context may
// already be cancelled when it is invoked.
//
// The uploadURL already contains embedded authentication, so it does not
// require an additional token.
func (cli *Client) cancelUploadSession(uploadURL string) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, uploadURL, nil)
	if err != nil {
		return
	}
	resp, err := cli.HTTPClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
