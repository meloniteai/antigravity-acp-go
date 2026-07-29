// Command antigravity-acp exposes agy as an ACP stdio server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	antigravityacp "github.com/shubzkothekar/antigravity-acp-go"
)

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	var (
		agyPath          string
		stateDir         string
		conversationsDir string
		workingDir       string
		version          string
		skipNarration    bool
		installAgy       bool
	)
	flag.StringVar(&agyPath, "agy", env("AGY_BIN", ""), "path to the agy executable")
	flag.StringVar(&stateDir, "state-dir", env("AGY_ACP_STATE_DIR", defaultStateDir()), "directory for ACP session state and downloaded agy")
	flag.StringVar(&conversationsDir, "conversations-dir", env("AGY_CONVERSATIONS_DIR", defaultConversationsDir()), "agy conversation database directory")
	flag.StringVar(&workingDir, "working-dir", "", "default working directory for sessions")
	flag.StringVar(&version, "version", "dev", "ACP agent version to advertise")
	flag.BoolVar(&skipNarration, "skip-narration", false, "omit narrative stream updates")
	flag.BoolVar(&installAgy, "install-agy", true, "download a verified agy binary when one is not on PATH")
	flag.Parse()

	if flag.NArg() != 0 {
		log.Fatalf("unexpected arguments: %s", strings.Join(flag.Args(), " "))
	}
	if strings.TrimSpace(stateDir) == "" {
		log.Fatal("--state-dir must not be empty")
	}
	if strings.TrimSpace(conversationsDir) == "" {
		log.Fatal("--conversations-dir must not be empty")
	}
	if strings.TrimSpace(workingDir) == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			log.Fatalf("resolve working directory: %v", err)
		}
	}

	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		log.Fatalf("create state directory: %v", err)
	}
	agyPath, err := resolveAgy(agyPath, stateDir, installAgy)
	if err != nil {
		log.Fatal(err)
	}

	store := antigravityacp.NewSessionStore(filepath.Join(stateDir, "sessions.json"), stateDir)
	agent := antigravityacp.NewAgyAcpAgent(agyPath, conversationsDir, workingDir, skipNarration, version, store)
	if err := antigravityacp.NewServer(agent).Run(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatalf("ACP server: %v", err)
	}
}

func resolveAgy(configured, stateDir string, install bool) (string, error) {
	if configured = strings.TrimSpace(configured); configured != "" {
		return executable(configured)
	}
	if found, err := exec.LookPath("agy"); err == nil {
		return found, nil
	}
	if !install {
		return "", errors.New("agy is not on PATH; pass --agy or enable --install-agy")
	}

	binDir := filepath.Join(stateDir, "bin")
	if err := antigravityacp.EnsureAgy(antigravityacp.InstallOptions{
		DestDir: binDir,
		Log:     func(message string) { log.Print(message) },
		Warn:    func(message string) { log.Print(message) },
	}); err != nil {
		return "", fmt.Errorf("install agy: %w", err)
	}
	name := "agy"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return executable(filepath.Join(binDir, name))
}

func executable(path string) (string, error) {
	if !filepath.IsAbs(path) && !strings.ContainsRune(path, filepath.Separator) {
		if found, err := exec.LookPath(path); err == nil {
			path = found
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("resolve agy executable %q: %w", path, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("agy executable %q is not executable", path)
	}
	return path, nil
}

func defaultStateDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ".agy-acp"
	}
	return filepath.Join(dir, "agy-acp")
}

func defaultConversationsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".gemini/antigravity-cli/conversations"
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
