package main

import (
	"bytes"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestVersionCommand(t *testing.T) {
	originalVersion := version
	version = "1.2.3"
	t.Cleanup(func() { version = originalVersion })

	var output bytes.Buffer
	cmd := newVersionCommand()
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute version command: %v", err)
	}
	if got, want := output.String(), "1.2.3\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestVersionCommandRejectsArguments(t *testing.T) {
	cmd := newVersionCommand()
	cmd.SetArgs([]string{"unexpected"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("version command accepted an unexpected argument")
	}
}

func TestStartupLogIncludesVersion(t *testing.T) {
	originalVersion := version
	version = "1.2.3"
	t.Cleanup(func() { version = originalVersion })

	core, logs := observer.New(zap.InfoLevel)
	undo := zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(undo)

	logStartup()

	entries := logs.FilterMessage("DNS Controller starting...").All()
	if len(entries) != 1 {
		t.Fatalf("startup log count = %d, want 1", len(entries))
	}
	if got, want := entries[0].ContextMap()["version"], "1.2.3"; got != want {
		t.Fatalf("startup version = %v, want %q", got, want)
	}
}
