package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestConfigValidated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      config
		wantRecords []string
		wantErr     string
	}{
		{
			name:        "API token",
			config:      config{records: []string{" home.example.com "}, cloudflareAPIToken: " token "},
			wantRecords: []string{"home.example.com"},
		},
		{
			name:        "API key",
			config:      config{records: []string{"home.example.com"}, cloudflareAPIKey: "key", cloudflareEmail: "owner@example.com"},
			wantRecords: []string{"home.example.com"},
		},
		{
			name:    "no records",
			config:  config{cloudflareAPIToken: "token"},
			wantErr: "at least one DNS record is required",
		},
		{
			name:    "empty record",
			config:  config{records: []string{" "}, cloudflareAPIToken: "token"},
			wantErr: "DNS record at position 1 is empty",
		},
		{
			name:    "no credentials",
			config:  config{records: []string{"home.example.com"}},
			wantErr: "exactly one of cloudflare API token or API key is required",
		},
		{
			name:    "conflicting credentials",
			config:  config{records: []string{"home.example.com"}, cloudflareAPIToken: "token", cloudflareAPIKey: "key"},
			wantErr: "exactly one of cloudflare API token or API key is required",
		},
		{
			name:    "API key without email",
			config:  config{records: []string{"home.example.com"}, cloudflareAPIKey: "key"},
			wantErr: "cloudflare email is required with an API key",
		},
		{
			name:    "API token with email",
			config:  config{records: []string{"home.example.com"}, cloudflareAPIToken: "token", cloudflareEmail: "owner@example.com"},
			wantErr: "cloudflare email cannot be used with an API token",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := test.config.validated()
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("validated() error = %v, want error containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validated() returned an unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got.records, test.wantRecords) {
				t.Fatalf("validated() records = %v, want %v", got.records, test.wantRecords)
			}
		})
	}
}

func TestCommandFlags(t *testing.T) {
	t.Parallel()

	command := newCommand("1.2.3")
	for _, name := range []string{"records", "cloudflare-api-token", "cloudflare-api-key", "cloudflare-email"} {
		if command.Flags().Lookup(name) == nil {
			t.Errorf("newCommand() is missing --%s", name)
		}
	}
	for _, name := range []string{"config", "cloudflare-key", "cloudflare-auth-type"} {
		if command.Flags().Lookup(name) != nil {
			t.Errorf("newCommand() still defines removed flag --%s", name)
		}
	}

	if err := command.Flags().Set("records", "one.example.com,two.example.com"); err != nil {
		t.Fatalf("set --records: %v", err)
	}
	records, err := command.Flags().GetStringSlice("records")
	if err != nil {
		t.Fatalf("get --records: %v", err)
	}
	if want := []string{"one.example.com", "two.example.com"}; !reflect.DeepEqual(records, want) {
		t.Fatalf("--records = %v, want %v", records, want)
	}
}

func TestVersionCommand(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	command := newVersionCommand("1.2.3")
	command.SetOut(&output)

	if err := command.Execute(); err != nil {
		t.Fatalf("execute version command: %v", err)
	}
	if got, want := output.String(), "1.2.3\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestVersionCommandRejectsArguments(t *testing.T) {
	t.Parallel()

	command := newVersionCommand("1.2.3")
	command.SetArgs([]string{"unexpected"})

	if err := command.Execute(); err == nil {
		t.Fatal("version command accepted an unexpected argument")
	}
}

func TestStartupLogIncludesVersion(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	undo := zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(undo)

	logStartup("1.2.3")

	entries := logs.FilterMessage("DNS Controller starting").All()
	if len(entries) != 1 {
		t.Fatalf("startup log count = %d, want 1", len(entries))
	}
	if got, want := entries[0].ContextMap()["version"], "1.2.3"; got != want {
		t.Fatalf("startup version = %v, want %q", got, want)
	}
}
