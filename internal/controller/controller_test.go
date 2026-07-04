package controller

import "testing"

func TestNewControllerValidation(t *testing.T) {
	t.Parallel()

	if _, err := NewController("", "", "token"); err == nil {
		t.Fatal("NewController() accepted an empty API token")
	}

	if _, err := NewController("api-key", "", "key"); err == nil {
		t.Fatal("NewController() accepted key authentication without an email")
	}

	controller, err := NewController("api-token", "", "token")
	if err != nil {
		t.Fatalf("NewController() returned an unexpected error: %v", err)
	}
	if controller.authType != "token" {
		t.Fatalf("NewController() authType = %q, want token", controller.authType)
	}
}

func TestApexFromFQDN(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"home.example.com": "example.com",
		"example.com":      "example.com",
		"localhost":        "localhost",
	}
	for fqdn, want := range tests {
		if got := apexFromFQDN(fqdn); got != want {
			t.Errorf("apexFromFQDN(%q) = %q, want %q", fqdn, got, want)
		}
	}
}
