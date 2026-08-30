# API: internal/graph

> Auto-generated with `go doc -all`. Date: 2026-08-30 13:04:27

```
package graph // import "github.com/frosado/onecloudriver/internal/graph"

Package graph provides an HTTP client for interacting with the Microsoft Graph
API (OneDrive). Supports CRUD operations on files and folders, upload/download
with streaming, async copy, retries with exponential backoff, and optimistic
concurrency control via ETag/If-Match.

CONSTANTS

const (
	DriveTypePersonal   = "personal"
	DriveTypeBusiness   = "business"
	DriveTypeSharepoint = "documentLibrary"
)
    DriveTypePersonal and friends represent the possible different values for a
    drive's type when fetched from the API.

const DefaultBaseURL = "https://graph.microsoft.com/v1.0"
    DefaultBaseURL is the official Microsoft Graph v1.0 base URL


VARIABLES

var (
	ErrItemNotFound  = errors.New("Item not found")
	ErrInvalidToken  = errors.New("Invalid token")
	ErrEmptyName     = errors.New("The name cannot be empty")
	ErrEmptyResource = errors.New("The resource cannot be empty")
	ErrNilContent    = errors.New("The content cannot be nil")
	ErrInvalidName   = errors.New("Invalid item name")
	ErrThrottled     = errors.New("Too many requests to the API") // new (429)
	ErrConflict      = errors.New("Conflict while modifying")     // new (409)
	// ErrPreconditionFailed is returned when an optimistic concurrency
	// control check fails (HTTP 412): the item changed on the server since
	// the ETag we sent in If-Match was read.
	ErrPreconditionFailed = errors.New("Precondition failed") // new (412)
)

FUNCTIONS

func ChildrenPathByID(id string) string
    ChildrenPathByID returns the resource path for listing children of an item
    by ID.

func ChildrenPathByPath(p string) string
    ChildrenPathByPath returns the resource path for listing children of an item
    by path.

func ContentPathByID(id string) string
    ContentPathByID returns the resource path for downloading content of an item
    by ID.

func ContentPathByPath(p string) string
    ContentPathByPath returns the resource path for downloading content of an
    item by path.

func DeltaPath() string
    DeltaPath returns the resource path for the delta endpoint of the drive
    root.

func QuickXORHashStream(r io.Reader) (string, error)
    QuickXORHashStream hashes the contents of r and returns the base64
    representation of the quickXorHash, matching the format the Microsoft Graph
    API uses for the file.hashes.quickXorHash field.

func ResourcePathByID(id string) string
    ResourcePathByID returns the resource path of an item addressed by ID.

        "root"      -> /me/drive/root
        "01ABC..."  -> /me/drive/items/01ABC...

func ResourcePathByPath(p string) string
    ResourcePathByPath returns the resource path of an item addressed by path.

        "/"                      -> /me/drive/root
        "/Documents/photo.jpg"   -> /me/drive/root:/Documents/photo.jpg

func SumQuickXORHash(data []byte) string
    SumQuickXORHash computes the quickXorHash of data and returns its base64
    representation, matching the format the Microsoft Graph API uses for the
    file.hashes.quickXorHash field.

func WithAction(resource, action string) string
    WithAction adds a navigation/action ("children", "content", "delta"...) to a
    resource path, respecting the path-based addressing syntax.

        /me/drive/root                    + children -> /me/drive/root/children
        /me/drive/items/123               + children -> /me/drive/items/123/children
        /me/drive/root:/Documents         + children -> /me/drive/root:/Documents:/children


TYPES

type AsyncOperationStatus struct {
	Status   string      `json:"status"` // "inProgress", "completed", "failed"
	Resource *DriveItem  `json:"resource,omitempty"`
	Error    *GraphError `json:"error,omitempty"`
}
    AsyncOperationStatus represents the status of an asynchronous Graph
    operation

type Client struct {
	BaseURL     string
	HTTPClient  HTTPDoer
	PollBackoff time.Duration // initial backoff for WaitForAsyncOperation (default: 1s)
}
    Client is the HTTP client for interacting with the Microsoft Graph API.
    Uses HTTPDoer instead of *http.Client to allow dependency injection.

func NewClient(opts ...Option) *Client
    NewClient creates a new client with default production configuration.
    Includes 3 retries with exponential backoff for transient network errors
    (timeout, DNS, connection refused) and HTTP codes 429/503. Use WithRetry(0)
    to disable retries.

        // Production (default values):
        client := graph.NewClient()

        // Tests with httptest server:
        client := graph.NewClient(graph.WithBaseURL(server.URL), graph.WithHTTPClient(server.Client()))

func (cli *Client) CopyItem(ctx context.Context, tokenProvider types.TokenProvider, item Resource, newName string, newParent Resource) (string, error)
    CopyItem copies an item (file or folder) to a new location and/or name in
    OneDrive.

    The copy operation is asynchronous in Microsoft Graph. Returns the
    monitoring URL (from the Location header) for polling progress.

    Parameters:
      - item: Resource of the item to copy
      - newName: new name for the copy (empty = keep the original)
      - newParent: Resource of the destination folder (empty = same folder)

    At least one of newName or newParent must be specified.

    Example:

        monitorURL, err := client.CopyItem(ctx, account, graph.ItemID("file123"), "copy.pdf", graph.ItemID("folder456"))
        if err != nil {
            return err
        }
        fmt.Println("Monitoring at:", monitorURL)

func (cli *Client) CreateFolder(ctx context.Context, tokenProvider types.TokenProvider, parent Resource, name string) (*DriveItem, error)
    CreateFolder creates a new folder inside the specified parent directory.

    The parent parameter can be an ItemID or ItemPath pointing to the container
    folder.

    Example:

        folder, err := client.CreateFolder(ctx, account, graph.ItemID("folder123"), "New Folder")
        if err != nil {
            return err
        }
        fmt.Println("Created:", folder.Name)

func (cli *Client) DeleteItem(ctx context.Context, tokenProvider types.TokenProvider, r Resource, etag string) error
    DeleteItem deletes an item (file or folder) from OneDrive.

    Accepts a Resource (ItemID or ItemPath) to identify the item to delete.
    The etag parameter enables optimistic concurrency control: if not empty,
    it is sent as an If-Match header to avoid deleting an outdated version.
    Returns nil if the operation was successful.

    Example:

        err := client.DeleteItem(ctx, account, graph.ItemID("file123"), "")
        if err != nil {
            return err
        }

func (cli *Client) GetItem(ctx context.Context, tokenProvider types.TokenProvider, res Resource) (*DriveItem, error)
    GetItem retrieves a DriveItem by its ID.

    The itemID parameter can be:
      - The ID of any DriveItem (e.g.: "01BYE5RZ6QN3VXWN...")
      - The string "root" to reference the root item

    Example:

        item, err := client.GetItem(ctx, account, "01BYE5RZ6QN3VXWN...")
        if err != nil {
            return err
        }
        fmt.Println("Name:", item.Name)

func (cli *Client) GetItemContent(ctx context.Context, tokenProvider types.TokenProvider, r Resource) ([]byte, error)
    GetItemContent downloads the binary content of a file from OneDrive.

    Accepts a Resource (ItemID or ItemPath) to address the file. Returns the
    file content bytes.

    Example:

        content, err := client.GetItemContent(ctx, account, graph.ItemID("01BYE5RZ..."))
        if err != nil {
            return err
        }
        err = os.WriteFile("local_file.pdf", content, 0644)

func (cli *Client) GetItemContentStream(ctx context.Context, tokenProvider types.TokenProvider, r Resource, output io.Writer) (int64, error)
    GetItemContentStream downloads the binary content of a file by writing it
    to an io.Writer. Uses Range requests in 10 MB chunks, which works for both
    small files (the loop runs a single iteration) and large files.

    Returns the total number of bytes written and an error if something fails.

    Example:

        file, _ := os.Create("large_file.zip")
        defer file.Close()
        n, err := client.GetItemContentStream(ctx, account, graph.ItemID("01BYE5RZ..."), file)
        if err != nil {
            return err
        }
        fmt.Printf("Downloaded %d bytes\n", n)

func (cli *Client) GetUser(ctx context.Context, tokenProvider types.TokenProvider) (*User, error)
    GetUser obtains the information of the user who owns the access token.

    Makes a request to /me of Microsoft Graph to get the authenticated user's
    profile.

    The tokenProvider is used to obtain the access token, allowing automatic
    refresh if the token expires during the operation.

    Example:

        user, err := client.GetUser(ctx, account)
        if err != nil {
            return err
        }
        fmt.Println("User:", user.DisplayName)

func (cli *Client) ListChildren(ctx context.Context, tokenProvider types.TokenProvider, r Resource) ([]DriveItem, error)
    ListChildren retrieves the child items of a specific folder by its ID.

    The itemID parameter can be:
      - A DriveItem ID (e.g.: "01BYE5RZ6QN3VXWN...")
      - The string "root" to reference the root folder

    Automatically handles pagination.

    Example:

        // List the contents of a folder
        children, err := client.ListChildren(ctx, account, folderItem.ID)
        if err != nil {
            return err
        }
        for _, child := range children {
            fmt.Println(child.Name)
        }

func (cli *Client) ListDriveRoot(ctx context.Context, tokenProvider types.TokenProvider) ([]DriveItem, error)
    ListDriveRoot retrieves the items at the root of the user's OneDrive.

    Automatically handles pagination by following @odata.nextLink links until
    all available items are obtained.

    The tokenProvider is used to obtain the access token on each request,
    allowing automatic refresh if the token expires during the operation.

func (cli *Client) MoveItem(ctx context.Context, tokenProvider types.TokenProvider, item Resource, newParent Resource, etag string) (*DriveItem, error)
    MoveItem moves an item (file or folder) to a new parent folder in OneDrive.

    Accepts:
      - item: the Resource (ItemID or ItemPath) of the item to move
      - newParent: the Resource (ItemID or ItemPath) of the destination folder
      - etag: optimistic concurrency control (empty = no control)

    If newParent is an ItemID, the body includes {"id": "..."}. If newParent is
    an ItemPath, the body includes {"path": "..."}.

    Example:

        item, err := client.MoveItem(ctx, account, graph.ItemID("file123"), graph.ItemID("folder456"), item.ETag)
        if err != nil {
            return err
        }
        fmt.Println("Moved to:", item.Parent.ID)

func (cli *Client) OverwriteItem(ctx context.Context, tokenProvider types.TokenProvider, itemID string, content io.Reader, etag string) (*DriveItem, error)
    OverwriteItem replaces the content of an existing item addressed by ID.

    Unlike UploadItem (which creates a new file inside a destination folder),
    this method targets the item's own /content endpoint, so it overwrites
    the item in place. Supports files up to 4 MB; for larger files use
    OverwriteItemStream.

    Parameters:
      - itemID: ID of the existing item to overwrite
      - content: io.Reader with the new file content
      - etag: optimistic concurrency control (empty = no control). When not
        empty, it is sent as an If-Match header; a changed remote item returns
        412 Precondition Failed (see ErrPreconditionFailed).

func (cli *Client) OverwriteItemStream(ctx context.Context, tokenProvider types.TokenProvider, itemID string, content io.Reader, fileSize int64, etag string) (*DriveItem, error)
    OverwriteItemStream replaces the content of an existing item (addressed by
    ID) using an upload session, for files larger than 4 MB.

    Parameters:
      - itemID: ID of the existing item to overwrite
      - content: io.Reader with the new file content
      - fileSize: total file size in bytes
      - etag: optimistic concurrency control (empty = no control).

func (cli *Client) PollDelta(ctx context.Context, tokenProvider types.TokenProvider, link string) ([]DeltaItem, string, bool, error)
    PollDelta queries the OneDrive delta endpoint. If link is "", it starts
    from the beginning (no token). Handles pagination: if the response includes
    @odata.nextLink, it returns the items and cont=true so the caller keeps
    paging. If it includes @odata.deltaLink, it's the last page: cont=false and
    nextLink contains the delta link for the next polling cycle.

    Example usage with pagination:

        link := ""
        for {
            items, nextLink, cont, err := client.PollDelta(ctx, tp, link)
            if err != nil { break }	//    // process items...
            link = nextLink
            if !cont { break }
        }

func (cli *Client) RenameItem(ctx context.Context, tokenProvider types.TokenProvider, r Resource, newName string, etag string) (*DriveItem, error)
    RenameItem renames an item (file or folder) in OneDrive.

    Accepts a Resource (ItemID or ItemPath) to identify the item to rename.
    The etag parameter enables optimistic concurrency control: if not empty,
    it is sent as an If-Match header to avoid renaming an outdated version.
    Returns the DriveItem updated with the new name.

    Example:

        item, err := client.RenameItem(ctx, account, graph.ItemID("file123"), "new-name.pdf", item.ETag)
        if err != nil {
            return err
        }
        fmt.Println("Renamed to:", item.Name)

func (cli *Client) URL(resourcePath string, query url.Values) string
    URL builds an absolute Microsoft Graph URL from a resource path and optional
    query parameters.

func (cli *Client) UploadItem(ctx context.Context, tokenProvider types.TokenProvider, parent Resource, fileName string, content io.Reader, etag string) (*DriveItem, error)
    UploadItem uploads a file to OneDrive via a simple PUT request.

    Supports files up to 4 MB. For larger files, use UploadItemStream.

    Parameters:
      - parent: Resource (ItemID or ItemPath) of the destination folder
      - fileName: name of the file to create in OneDrive
      - content: io.Reader with the file content
      - etag: optimistic concurrency control (empty = no control). When not
        empty, it is sent as an If-Match header so the upload only overwrites
        the server item if it still matches this ETag; otherwise the API returns
        412 Precondition Failed (see ErrPreconditionFailed).

    Example:

        file, _ := os.Open("photo.jpg")
        defer file.Close()
        item, err := client.UploadItem(ctx, account, graph.ItemID("folder123"), "photo.jpg", file, "")
        if err != nil {
            return err
        }
        fmt.Println("Uploaded:", item.Name)

func (cli *Client) UploadItemStream(ctx context.Context, tokenProvider types.TokenProvider, parent Resource, fileName string, content io.Reader, fileSize int64, etag string) (*DriveItem, error)
    UploadItemStream uploads a large file to OneDrive using upload sessions.

    Creates an upload session and uploads the file in 320 KiB chunks (minimum
    required). Unlike UploadItem, it requires knowing the total file size.

    Parameters:
      - content: io.Reader with the file content
      - fileSize: total file size in bytes
      - etag: optimistic concurrency control (empty = no control). When not
        empty, it is sent as an If-Match header on the createUploadSession
        request so the upload only replaces the server item if it still matches
        this ETag; otherwise the API returns 412 Precondition Failed.

    Example:

        file, _ := os.Open("large_file.zip")
        defer file.Close()
        stat, _ := file.Stat()
        item, err := client.UploadItemStream(ctx, account, graph.ItemID("folder123"), "large.zip", file, stat.Size(), "")

func (cli *Client) WaitForAsyncOperation(ctx context.Context, monitorURL string) (*DriveItem, error)
    WaitForAsyncOperation polls the monitoring URL until the operation
    completes. Uses exponential backoff (1s, 2s, 4s...) and respects context
    cancellation.

type Deleted struct {
	State string `json:"state,omitempty"`
}
    Deleted is used for detecting when items get deleted on the server
    https://docs.microsoft.com/en-us/onedrive/developer/rest-api/resources/deleted

type DeletedState struct {
	State string `json:"state,omitempty"`
}
    DeletedState indicates that an item was deleted on the server. Used in
    DeltaItem to distinguish deletions from other operations.

type DeltaItem struct {
	DriveItem
	Deleted *DeletedState `json:"deleted,omitempty"`
}
    DeltaItem is an item returned by the Microsoft Graph delta endpoint.
    Adds the Deleted field (absent in normal DriveItem) to detect deletions.

type DriveItem struct {
	ID               string           `json:"id,omitempty"`
	Name             string           `json:"name,omitempty"`
	Size             uint64           `json:"size,omitempty"`
	ModTime          *time.Time       `json:"lastModifiedDatetime,omitempty"`
	Parent           *DriveItemParent `json:"parentReference,omitempty"`
	Folder           *Folder          `json:"folder,omitempty"`
	File             *File            `json:"file,omitempty"`
	Deleted          *Deleted         `json:"deleted,omitempty"`
	ConflictBehavior string           `json:"@microsoft.graph.conflictBehavior,omitempty"`
	ETag             string           `json:"eTag,omitempty"`
	CreatedTime      *time.Time       `json:"createdDateTime,omitempty"`
}
    DriveItem contains the data fields from the Graph API
    https://docs.microsoft.com/en-us/onedrive/developer/rest-api/resources/driveitem

func (drive *DriveItem) IsFolder() bool
    IsFolder returns if the DriveItem represents a directory or not

func (drive *DriveItem) ModTimeString() string

func (drive *DriveItem) ModTimeUnix() uint64
    ModTimeUnix returns the modification time as a unix uint64 time

    security: gosec (G115) flags this int64->uint64 conversion as an overflow
    risk. A negative time.Time.Unix() (date before 1970) would wrap to an
    absurdly large uint64. It's not exploitable as a vulnerability (no RCE or
    privilege escalation, just an incorrect date shown to the user), but the
    date metadata comes from the Microsoft Graph server, so we protect ourselves
    with a minimal guard for data robustness, not because it's a real attack
    surface.

func (item *DriveItem) VerifyChecksum(checksum string) bool
    VerifyChecksum reports whether checksum (a locally computed base64
    quickXorHash) matches the hash the server stored for this item. The
    comparison is case-insensitive and returns false when either side is empty
    or when the item has no file metadata (folders never carry a quickXorHash).

type DriveItemParent struct {
	// TODO Path is technically available, but we shouldn't use it
	Path      string `json:"path,omitempty"`
	ID        string `json:"id,omitempty"`
	DriveID   string `json:"driveId,omitempty"`
	DriveType string `json:"driveType,omitempty"` // personal | business | documentLibrary
}
    DriveItemParent describes a DriveItem's parent in the Graph API
    https://docs.microsoft.com/en-us/onedrive/developer/rest-api/resources/itemreference

type File struct {
	Hashes Hashes `json:"hashes,omitempty"`
}
    File is used for checking for changes in
    local files (relative to the server).
    https://docs.microsoft.com/en-us/onedrive/developer/rest-api/resources/file

type Folder struct {
	ChildCount uint32 `json:"childCount,omitempty"`
}
    Folder is used for parsing only
    https://docs.microsoft.com/en-us/onedrive/developer/rest-api/resources/folder

type GraphError struct {
	StatusCode int    // HTTP code (e.g.: 404, 401)
	Code       string `json:"code"`    // Graph error code (e.g.: "itemNotFound")
	Message    string `json:"message"` // Descriptive error message
}
    GraphError represents an error returned by the Microsoft Graph API.

func (e *GraphError) Error() string

func (e *GraphError) Is(target error) bool
    Is allows using errors.Is to check sentinel errors by HTTP code.

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}
    HTTPDoer is the minimal interface that Client needs to execute HTTP
    requests. *http.Client satisfies it automatically, allowing mock injection
    in tests.

type Hashes struct {
	SHA1Hash     string `json:"sha1Hash,omitempty"`
	QuickXorHash string `json:"quickXorHash,omitempty"`
}
    Hashes are integrity hashes used to determine if file content has changed.
    https://docs.microsoft.com/en-us/onedrive/developer/rest-api/resources/hashes

type ItemID string
    ItemID identifies a OneDrive resource by its unique ID. Example:
    graph.ItemID("01BYE5RZ6QN3VXWN...")

const RootID ItemID = "root"
    RootID is the special ID that references the drive's root folder.

func (id ItemID) IsEmpty() bool
    IsEmpty returns true if ItemID is an empty string.

func (id ItemID) ParentReference() map[string]any
    ParentReference returns a map {"id": "..."} for use in MoveItem/CopyItem.

func (id ItemID) ResourcePath() string
    ResourcePath returns the Graph resource path for this ItemID.

type ItemPath string
    ItemPath identifies a OneDrive resource by its path within the drive.
    Example: graph.ItemPath("/Documents/photo.jpg")

func (p ItemPath) IsEmpty() bool
    IsEmpty returns true if ItemPath is an empty string.

func (p ItemPath) ParentReference() map[string]any
    ParentReference returns a map {"path": "/..."} for use in MoveItem/CopyItem.

func (p ItemPath) ResourcePath() string
    ResourcePath returns the Graph resource path for this ItemPath.

type Option func(*Client)
    Option is a function that configures a Client. Follows the Functional
    Options pattern.

func WithBaseURL(u string) Option
    WithBaseURL configures the Microsoft Graph base URL.

func WithHTTPClient(h HTTPDoer) Option
    WithHTTPClient allows injecting a custom HTTP client (useful for tests).

func WithRetry(maxRetries int) Option
    WithRetry wraps the current HTTPClient in a RetryDoer with the specified
    number of retries. Should be applied after WithHTTPClient/WithTimeout.

func WithTimeout(d time.Duration) Option
    WithTimeout configures the HTTP client timeout. If the current HTTPClient
    is an *http.Client (the default case), it modifies its Timeout while
    preserving any Transport or additional configuration that has been set,
    and also applies the same timeout as the transport's ResponseHeaderTimeout
    so a stalled response-header read is bounded too (issue #70).

func WithTransport(t *http.Transport) Option
    WithTransport replaces the *http.Transport used by the client's
    *http.Client. It is a no-op when the current HTTPClient is not an
    *http.Client (e.g. a mock injected via WithHTTPClient), in which case the
    transport is irrelevant. Useful for tests (connection-reuse assertions) and
    custom pooling overrides.

type Resource interface {
	ResourcePath() string
	IsEmpty() bool
	ParentReference() map[string]any
}
    URL builds an absolute URL from a resource path and optional parameters.
    Resource identifies a OneDrive item, either by ID or by path. Implemented by
    ItemID and ItemPath.

type RetryDoer struct {
	// Has unexported fields.
}
    RetryDoer wraps an HTTPDoer with retry logic using exponential backoff.
    Automatically retries on 429 (Too Many Requests) and 503 (Service
    Unavailable). Respects the Retry-After header if present; otherwise uses
    exponential backoff.

func NewRetryDoer(inner HTTPDoer, maxRetries int) *RetryDoer
    NewRetryDoer creates a RetryDoer that retries up to maxRetries additional
    times (max 1 + maxRetries total attempts).

func (r *RetryDoer) Do(req *http.Request) (*http.Response, error)
    Do implements HTTPDoer with retries for transient network errors and HTTP
    codes 429/503. Uses exponential backoff with Retry-After as priority.

type User struct {
	ID                string `json:"id"`                          // Unique user ID
	UserPrincipalName string `json:"userPrincipalName,omitempty"` // e.g.: user@outlook.com
	DisplayName       string `json:"displayName,omitempty"`       // e.g.: John Doe
	Mail              string `json:"mail,omitempty"`              // Primary email (may differ from UPN)
	GivenName         string `json:"givenName,omitempty"`         // First name
	Surname           string `json:"surname,omitempty"`           // Last name
}
    User represents the basic Microsoft Graph user information.

    Contains the authenticated user's profile information. Corresponds to the
    /me resource of the Microsoft Graph API.

    Documentation: https://learn.microsoft.com/en-us/graph/api/user-get

```
