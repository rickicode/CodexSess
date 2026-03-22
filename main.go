package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/ricki/codexsess/internal/config"
	icrypto "github.com/ricki/codexsess/internal/crypto"
	"github.com/ricki/codexsess/internal/httpapi"
	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/trafficlog"
	"github.com/ricki/codexsess/internal/util"
	"golang.org/x/term"
)

var appVersion = "dev"

func main() {
	if err := run(); err != nil {
		log.Printf("codexsess: %v", err)
		waitForExitOnWindowsError()
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 {
		switch strings.TrimSpace(os.Args[1]) {
		case "--changepassword":
			return changePassword()
		case "--version":
			fmt.Println(effectiveAppVersion())
			return nil
		default:
			return fmt.Errorf("unknown argument: %s", os.Args[1])
		}
	}

	cfg, err := config.LoadOrInit()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("prepare data dir: %w", err)
	}
	if err := os.MkdirAll(cfg.AuthStoreDir, 0o700); err != nil {
		return fmt.Errorf("prepare auth store dir: %w", err)
	}
	if err := os.MkdirAll(cfg.CodexHome, 0o700); err != nil {
		return fmt.Errorf("prepare codex home dir: %w", err)
	}

	key, err := util.LoadOrCreateMasterKey(cfg.MasterKeyPath)
	if err != nil {
		return fmt.Errorf("load master key: %w", err)
	}
	cry, err := icrypto.New(key)
	if err != nil {
		return fmt.Errorf("init crypto: %w", err)
	}

	st, err := store.Open(filepath.Join(cfg.DataDir, "data.db"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	traffic, err := trafficlog.New(filepath.Join(cfg.DataDir, "traffic.log"), 2*1024*1024)
	if err != nil {
		return fmt.Errorf("init traffic logger: %w", err)
	}

	svc := service.New(cfg, st, cry)
	resolvedCodexBin, codexVersion, err := ensureCodexCLIReady(svc.Codex.Binary)
	if err != nil {
		return err
	}
	svc.Codex.Binary = resolvedCodexBin
	srv := httpapi.New(
		svc,
		cfg.BindAddr,
		cfg.ProxyAPIKey,
		cfg.AdminUsername,
		cfg.AdminPasswordHash,
		traffic,
		effectiveAppVersion(),
		codexVersion,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	appURL := localAppURL(cfg.BindAddr)
	log.Printf("codexsess bind address %s", strings.TrimSpace(cfg.BindAddr))
	if bindIsPublic(cfg.BindAddr) {
		log.Printf("codexsess access scope public")
	} else {
		log.Printf("codexsess access scope local")
	}
	log.Printf("codexsess local console %s", appURL)
	if shouldAutoOpenBrowser() {
		go waitAndOpenBrowser(appURL)
	}
	err = srv.ListenAndServe(ctx)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func ensureCodexCLIReady(binary string) (string, string, error) {
	name := strings.TrimSpace(binary)
	if name == "" {
		name = "codex"
	}
	path, err := resolveCodexPath(name)
	if err != nil {
		return "", "", fmt.Errorf("codex CLI not found in PATH. install with: npm i -g @openai/codex")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("codex CLI is not executable (%s). verify installation and PATH", path)
	}
	version := strings.TrimSpace(string(out))
	if idx := strings.IndexByte(version, '\n'); idx >= 0 {
		version = strings.TrimSpace(version[:idx])
	}
	if version == "" {
		version = "unknown"
	}
	return path, version, nil
}

func resolveCodexPath(binary string) (string, error) {
	name := strings.TrimSpace(binary)
	if name == "" {
		name = "codex"
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	if runtime.GOOS != "windows" {
		return "", exec.ErrNotFound
	}
	if strings.ContainsAny(name, `\/`) {
		return "", exec.ErrNotFound
	}
	candidates := []string{name + ".cmd", name + ".exe", name + ".bat"}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}

func effectiveAppVersion() string {
	v := strings.TrimSpace(appVersion)
	if v != "" && !strings.EqualFold(v, "dev") {
		return v
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		mv := strings.TrimSpace(bi.Main.Version)
		if mv != "" && mv != "(devel)" {
			return mv
		}
	}
	return "dev"
}

func shouldAutoOpenBrowser() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("CODEXSESS_NO_OPEN_BROWSER")))
	return raw != "1" && raw != "true" && raw != "yes"
}

func localAppURL(bindAddr string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(bindAddr))
	if err != nil {
		trimmed := strings.TrimSpace(bindAddr)
		if strings.HasPrefix(trimmed, ":") {
			return "http://127.0.0.1" + trimmed
		}
		return "http://127.0.0.1:3061"
	}
	h := strings.TrimSpace(host)
	if h == "" || h == "0.0.0.0" || h == "::" || h == "[::]" {
		h = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%s", h, port)
}

func bindIsPublic(bindAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(bindAddr))
	if err != nil {
		return false
	}
	h := strings.Trim(strings.TrimSpace(host), "[]")
	return h == "0.0.0.0" || h == "::"
}

func waitAndOpenBrowser(appURL string) {
	healthURL := strings.TrimRight(appURL, "/") + "/healthz"
	client := &http.Client{Timeout: 700 * time.Millisecond}
	for i := 0; i < 20; i++ {
		resp, err := client.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				if err := openDefaultBrowser(appURL); err != nil {
					log.Printf("failed to open browser automatically: %v", err)
				} else {
					log.Printf("opened browser: %s", appURL)
				}
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func openDefaultBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func changePassword() error {
	cfg, err := config.LoadOrInit()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Username [%s]: ", cfg.AdminUsername)
	userInput, _ := reader.ReadString('\n')
	user := strings.TrimSpace(userInput)
	if user == "" {
		user = cfg.AdminUsername
	}

	fmt.Print("New password: ")
	pass1Bytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	pass1 := strings.TrimSpace(string(pass1Bytes))
	if pass1 == "" {
		return fmt.Errorf("password cannot be empty")
	}

	fmt.Print("Confirm new password: ")
	pass2Bytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("read password confirmation: %w", err)
	}
	pass2 := strings.TrimSpace(string(pass2Bytes))
	if pass1 != pass2 {
		return fmt.Errorf("password confirmation mismatch")
	}

	cfg.AdminUsername = user
	cfg.AdminPasswordHash = config.HashPassword(pass1)
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println("Admin credential updated successfully.")
	return nil
}

func waitForExitOnWindowsError() {
	if runtime.GOOS != "windows" {
		return
	}
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("CODEXSESS_NO_PAUSE_ON_ERROR")))
	if raw == "1" || raw == "true" || raw == "yes" {
		return
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		_ = exec.Command("cmd", "/c", "pause").Run()
		return
	}
	fmt.Print("Press Enter to exit...")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		_ = exec.Command("cmd", "/c", "pause").Run()
	}
}
