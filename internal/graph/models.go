package graph

import "time"

// DriveTypePersonal and friends represent the possible different values for a
// drive's type when fetched from the API.
const (
	DriveTypePersonal   = "personal"
	DriveTypeBusiness   = "business"
	DriveTypeSharepoint = "documentLibrary"
)

// DriveItemParent describes a DriveItem's parent in the Graph API
// https://docs.microsoft.com/en-us/onedrive/developer/rest-api/resources/itemreference
type DriveItemParent struct {
	//TODO Path is technically available, but we shouldn't use it
	Path      string `json:"path,omitempty"`
	ID        string `json:"id,omitempty"`
	DriveID   string `json:"driveId,omitempty"`
	DriveType string `json:"driveType,omitempty"` // personal | business | documentLibrary
}

// Folder is used for parsing only
// https://docs.microsoft.com/en-us/onedrive/developer/rest-api/resources/folder
type Folder struct {
	ChildCount uint32 `json:"childCount,omitempty"`
}

// Hashes are integrity hashes used to determine if file content has changed.
// https://docs.microsoft.com/en-us/onedrive/developer/rest-api/resources/hashes
type Hashes struct {
	SHA1Hash     string `json:"sha1Hash,omitempty"`
	QuickXorHash string `json:"quickXorHash,omitempty"`
}

// File is used for checking for changes in local files (relative to the server).
// https://docs.microsoft.com/en-us/onedrive/developer/rest-api/resources/file
type File struct {
	Hashes Hashes `json:"hashes,omitempty"`
}

// Deleted is used for detecting when items get deleted on the server
// https://docs.microsoft.com/en-us/onedrive/developer/rest-api/resources/deleted
type Deleted struct {
	State string `json:"state,omitempty"`
}

type createFolderRequest struct {
	Name   string   `json:"name"`
	Folder struct{} `json:"folder"`
}

type renameItemRequest struct {
	Name string `json:"name"`
}

type moveItemRequest struct {
	ParentReference map[string]any `json:"parentReference"`
}

type copyItemRequest struct {
	Name            string         `json:"name,omitempty"`
	ParentReference map[string]any `json:"parentReference,omitempty"`
}

type createUploadSessionRequest struct {
	Item struct {
		ConflictBehavior string `json:"@microsoft.graph.conflictBehavior"`
	} `json:"item"`
}

type createUploadSessionResponse struct {
	UploadURL string `json:"uploadUrl"`
}

// DriveItem contains the data fields from the Graph API
// https://docs.microsoft.com/en-us/onedrive/developer/rest-api/resources/driveitem
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

// IsFolder returns if the DriveItem represents a directory or not
func (drive *DriveItem) IsFolder() bool {
	return drive.Folder != nil
}

// ModTimeUnix returns the modification time as a unix uint64 time
//
// security: gosec (G115) flags this int64->uint64 conversion as an overflow
// risk. A negative time.Time.Unix() (date before 1970) would wrap to an
// absurdly large uint64. It's not exploitable as a vulnerability (no RCE or
// privilege escalation, just an incorrect date shown to the user), but the
// date metadata comes from the Microsoft Graph server, so we protect
// ourselves with a minimal guard for data robustness, not because it's a
// real attack surface.
func (drive *DriveItem) ModTimeUnix() uint64 {
	if drive.ModTime == nil {
		return 0
	}
	unix := drive.ModTime.Unix()
	if unix < 0 {
		return 0
	}
	return uint64(unix)
}

func (drive *DriveItem) ModTimeString() string {
	if drive.ModTime == nil {
		return "N/A"
	}
	return drive.ModTime.Format("2006-01-02 15:04:05")
}

// driveItemPage represents a Microsoft Graph response page
// containing a list of DriveItems and a link to the next page.
type driveItemPage struct {
	Value    []DriveItem `json:"value"`
	NextLink string      `json:"@odata.nextLink,omitempty"`
}
