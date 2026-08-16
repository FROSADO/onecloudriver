package graph

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net/http"

	"github.com/frosado/onecloudriver/internal/graph/quickxorhash"
	"github.com/frosado/onecloudriver/internal/types"
)

// GetItemContent downloads the binary content of a file from OneDrive.
//
// Accepts a Resource (ItemID or ItemPath) to address the file.
// Returns the file content bytes.
//
// Example:
//
//	content, err := client.GetItemContent(ctx, account, graph.ItemID("01BYE5RZ..."))
//	if err != nil {
//	    return err
//	}
//	err = os.WriteFile("local_file.pdf", content, 0644)
func (cli *Client) GetItemContent(ctx context.Context, tokenProvider types.TokenProvider, r Resource) ([]byte, error) {
	var buf bytes.Buffer
	_, err := cli.GetItemContentStream(ctx, tokenProvider, r, &buf)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GetItemContentStream downloads the binary content of a file by writing it
// to an io.Writer. Uses Range requests in 10 MB chunks, which works for
// both small files (the loop runs a single iteration) and large files.
//
// Returns the total number of bytes written and an error if something fails.
//
// Example:
//
//	file, _ := os.Create("large_file.zip")
//	defer file.Close()
//	n, err := client.GetItemContentStream(ctx, account, graph.ItemID("01BYE5RZ..."), file)
//	if err != nil {
//	    return err
//	}
//	fmt.Printf("Downloaded %d bytes\n", n)
func (cli *Client) GetItemContentStream(ctx context.Context, tokenProvider types.TokenProvider, r Resource, output io.Writer) (int64, error) {
	if err := validateResource(r); err != nil {
		return 0, err
	}

	// Get metadata to know the file size
	item, err := cli.GetItem(ctx, tokenProvider, r)
	if err != nil {
		return 0, fmt.Errorf("error getting item metadata: %w", err)
	}

	contentURL := cli.URL(WithAction(r.ResourcePath(), "content"), nil)
	const chunkSize int64 = 10 * 1024 * 1024 // 10 MB

	// Hash the streamed bytes so we can verify integrity against the
	// server-provided quickXorHash once the download completes.
	hasher := quickxorhash.New()
	output = io.MultiWriter(output, hasher)

	if item.Size > math.MaxInt64 {
		return 0, fmt.Errorf("file too large: %d bytes", item.Size)
	}
	fileSize := int64(item.Size)
	var totalWritten int64

	for start := int64(0); start < fileSize; start += chunkSize {
		select {
		case <-ctx.Done():
			return totalWritten, ctx.Err()
		default:
		}

		end := start + chunkSize - 1
		if end >= fileSize-1 {
			end = fileSize - 1
		}

		n, err := cli.downloadRange(ctx, tokenProvider, contentURL, start, end, output)
		totalWritten += n
		if err != nil {
			return totalWritten, fmt.Errorf("error downloading chunk %d-%d: %w", start, end, err)
		}
	}

	// Integrity check: reject a corrupt download. Skipped when the server
	// metadata carries no quickXorHash (folders and some files may lack it).
	if item.File != nil && item.File.Hashes.QuickXorHash != "" {
		computed := base64.StdEncoding.EncodeToString(hasher.Sum(nil))
		if !item.VerifyChecksum(computed) {
			return totalWritten, fmt.Errorf(
				"download integrity check failed: quickXorHash mismatch (server %s, computed %s)",
				item.File.Hashes.QuickXorHash, computed)
		}
	}

	return totalWritten, nil
}

// downloadRange downloads a specific byte range of a file using the HTTP
// Range header. Writes the received bytes directly to the io.Writer
// and returns the number of bytes written.
//
// Verifies that the response is 206 Partial Content and that the
// Content-Range matches the requested range.
func (cli *Client) downloadRange(ctx context.Context, tokenProvider types.TokenProvider, reqURL string, start, end int64, output io.Writer) (int64, error) {
	headers := map[string]string{
		"Range": fmt.Sprintf("bytes=%d-%d", start, end),
	}
	resp, err := cli.doAuthenticatedRequestWithBody(ctx, http.MethodGet, reqURL, nil, "", headers, tokenProvider)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return 0, err
	}

	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("unexpected response for Range: HTTP %d, expected 206", resp.StatusCode)
	}

	contentRange := resp.Header.Get("Content-Range")
	if contentRange == "" {
		return 0, fmt.Errorf("partial response without Content-Range header")
	}

	expected := end - start + 1
	written, err := io.Copy(output, resp.Body)
	if err != nil {
		return written, err
	}

	if written != expected {
		return written, fmt.Errorf("incomplete chunk: expected %d bytes, received %d", expected, written)
	}

	return written, nil
}
