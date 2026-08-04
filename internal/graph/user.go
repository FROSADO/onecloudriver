package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/frosado/onecloudriver/internal/types"
)

// User represents the basic Microsoft Graph user information.
//
// Contains the authenticated user's profile information.
// Corresponds to the /me resource of the Microsoft Graph API.
//
// Documentation: https://learn.microsoft.com/en-us/graph/api/user-get
type User struct {
	ID                string `json:"id"`                          // Unique user ID
	UserPrincipalName string `json:"userPrincipalName,omitempty"` // e.g.: user@outlook.com
	DisplayName       string `json:"displayName,omitempty"`       // e.g.: John Doe
	Mail              string `json:"mail,omitempty"`              // Primary email (may differ from UPN)
	GivenName         string `json:"givenName,omitempty"`         // First name
	Surname           string `json:"surname,omitempty"`           // Last name
}

// GetUser obtains the information of the user who owns the access token.
//
// Makes a request to /me of Microsoft Graph to get the authenticated
// user's profile.
//
// The tokenProvider is used to obtain the access token, allowing
// automatic refresh if the token expires during the operation.
//
// Example:
//
//	user, err := client.GetUser(ctx, account)
//	if err != nil {
//	    return err
//	}
//	fmt.Println("User:", user.DisplayName)
func (cli *Client) GetUser(ctx context.Context, tokenProvider types.TokenProvider) (*User, error) {
	// Make authenticated GET request (reuses helper from drive_item.go)
	resp, err := cli.doAuthenticatedRequest(ctx, http.MethodGet, cli.BaseURL+"/me", tokenProvider)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Validate response (checkResponse consumes the body only on error)
	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	// Parse JSON response
	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("error parsing Graph response: %w", err)
	}

	// Validate that we at least have the user's ID
	// (UserPrincipalName may be empty on personal Microsoft accounts)
	if user.ID == "" {
		return nil, fmt.Errorf("Graph response does not contain a valid user ID")
	}

	return &user, nil
}
