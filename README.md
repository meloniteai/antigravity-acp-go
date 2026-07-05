# Antigravity ACP Go Library

A Golang library implementation of the [Agent Client Protocol](https://agentclientprotocol.com) (ACP) server for Google Antigravity's `agy` CLI, written as a pure-Go module.

This library spawns the `agy` CLI, streams its progress live, and replays conversation history on demand. It features a zero-dependency custom Protobuf decoder and uses a Cgo-free SQLite driver (`modernc.org/sqlite`) to make cross-compilation across platforms (macOS, Linux, Windows, arm64, amd64) seamless.

## Installation

```bash
go get github.com/shubzkothekar/antigravity-acp-go
```

## Features

- **Standard ACP Implementation**: Supports `agent/initialize`, `session/new`, `session/load`, `session/resume`, `session/list`, `session/delete`, `session/close`, `session/prompt`, and `session/setConfigOption`.
- **Pure Go Protobuf Decoding**: Custom binary protobuf parser decodes steps payload, error details, permissions request, and task details columns out of SQLite databases with zero dependencies.
- **Asynchronous Loop Ticker**: Runs live database step checks in goroutines, enabling concurrency and immediate processing of client cancels.
- **Auto-Provisioning**: Automatically fetches and verifies SHA-256 signatures of release binaries of the `agy` CLI from GitHub.
- **CamelCase / SnakeCase Normalization**: Handles backwards compatibility for sessions files saved with older schema formats.

## Usage

Here is a simple example showing how to build an ACP server executable using the library:

```go
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	antigravityacp "github.com/shubzkothekar/antigravity-acp-go"
)

func main() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("failed to find user home dir: %v", err)
	}

	stateDir := filepath.Join(homeDir, ".agy-acp")
	sessionsFile := filepath.Join(stateDir, "sessions.json")
	store := antigravityacp.NewSessionStore(sessionsFile, stateDir)

	// Resolve or download agy binary path
	destDir := filepath.Join(stateDir, "bin")
	err = antigravityacp.EnsureAgy(antigravityacp.InstallOptions{
		DestDir: destDir,
		Log:     func(msg string) { log.Println(msg) },
		Warn:    func(msg string) { log.Println("WARN:", msg) },
	})
	if err != nil {
		log.Fatalf("failed to ensure agy binary: %v", err)
	}

	// Determine the path of the downloaded executable
	agyBin := filepath.Join(destDir, "agy")
	if os.Getenv("AGY_BIN") != "" {
		agyBin = os.Getenv("AGY_BIN")
	}

	convDir := filepath.Join(homeDir, ".gemini", "antigravity-cli", "conversations")
	if os.Getenv("AGY_CONVERSATIONS_DIR") != "" {
		convDir = os.Getenv("AGY_CONVERSATIONS_DIR")
	}

	agent := antigravityacp.NewAgyAcpAgent(agyBin, convDir, ".", false, "1.0.0", store)
	server := antigravityacp.NewServer(agent)

	log.Println("Starting ACP server on stdin/stdout...")
	if err := server.Run(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatalf("server terminated with error: %v", err)
	}
}
```

## Running Tests

Run the E2E and unit test suite:

```bash
go test -v ./...
```
