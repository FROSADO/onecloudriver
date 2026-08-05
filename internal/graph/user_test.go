package graph

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ExampleClient_GetUser demonstrates how to get the profile of the authenticated user.
func ExampleClient_GetUser() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "12345-abcde",
			"userPrincipalName": "testuser@outlook.com",
			"displayName": "Test User"
		}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	tokenProvider := &mockTokenProvider{token: "test_token"}

	user, err := client.GetUser(context.Background(), tokenProvider)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println(user.DisplayName)
	fmt.Println(user.UserPrincipalName)

	// Output:
	// Test User
	// testuser@outlook.com
}

// TestClient_GetUser_Success tests the successful retrieval of the user profile
func TestClient_GetUser_Success(t *testing.T) {
	// 1. Creamos un servidor HTTP falso que simula a Microsoft Graph
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// We validate that the request is correct
		if r.URL.Path != "/me" {
			t.Errorf("Expected path /me, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fake_token_123" {
			t.Errorf("Authorization token not sent correctly")
		}

		// We respond with a valid JSON
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "12345-abcde",
			"userPrincipalName": "testuser@outlook.com", 
			"displayName": "Test User",
			"mail": "testuser@outlook.com"
		}`))
	}))
	defer server.Close()

	// 2. Inyectamos el servidor falso en nuestro Cliente
	client := &Client{
		BaseURL:    server.URL, // The magic! The client thinks it is talking to Microsoft
		HTTPClient: server.Client(),
	}

	// 3. Mock de TokenProvider
	tokenProvider := &mockTokenProvider{token: "fake_token_123"}

	// 4. We execute the method
	user, err := client.GetUser(context.Background(), tokenProvider)

	// 5. Aserciones
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if user.ID != "12345-abcde" {
		t.Errorf("Incorrect ID: %s", user.ID)
	}
	if user.UserPrincipalName != "testuser@outlook.com" {
		t.Errorf("Incorrect user principal name: %s", user.UserPrincipalName)
	}
	if user.DisplayName != "Test User" {
		t.Errorf("Incorrect display name: %s", user.DisplayName)
	}
}

// TestClient_GetUser_Error tests error handling (e.g. invalid token)
func TestClient_GetUser_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulamos un error 401 Unauthorized con mensaje de Graph
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{
			"error": {
				"code": "InvalidAuthenticationToken",
				"message": "Access token has expired or is not yet valid."
			}
		}`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	// Mock de TokenProvider
	tokenProvider := &mockTokenProvider{token: "bad_token"}

	_, err := client.GetUser(context.Background(), tokenProvider)

	if err == nil {
		t.Fatal("An error was expected for an invalid token, but nil was obtained")
	}
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("Expected error ErrInvalidToken, got: %v", err)
	}
}
