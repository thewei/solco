// Command solco-hostd is Solco's resident Remote Host: it keeps the phone-facing Host v3 reachable while the
// desktop app is closed. It owns the port only while the desktop does not (internal/hostd's lease), reads the
// same settings, certificate and paired-device store as the app, and serves only what needs no window:
// this Mac's Herdr tree/terminals and the Assistants core.
//
//	solco-hostd run [-data DIR]          serve (what launchd runs)
//	solco-hostd install [-data DIR]      write and load ~/Library/LaunchAgents/com.whstudio.solco.hostd.plist
//	solco-hostd uninstall                unload and remove it
//	solco-hostd status                   print whether the service is loaded
//	solco-hostd feedback-mcp [-data DIR] the stdio MCP server external Agents use to ask the user (Solco Feedback)
//
// `run` also keeps the Feedback broker (internal/feedback) alive: it serves it whenever no other process does.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/thewei/solco/internal/feedback"
	"github.com/thewei/solco/internal/feedback/mcpshim"
	"github.com/thewei/solco/internal/hostd"
	"github.com/thewei/solco/internal/settings"
)

// version is stamped by the build (-ldflags "-X main.version=…").
var version = "hostd"

func defaultDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "Solco")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: solco-hostd run|install|uninstall|status|feedback-mcp [-data DIR]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "Solco user-data directory (settings.json, RemoteHost/)")
	_ = fs.Parse(os.Args[2:])
	var err error
	switch cmd {
	case "run":
		err = run(*data)
	case "install":
		err = hostd.InstallSelf(*data)
	case "uninstall":
		err = hostd.Uninstall()
	case "status":
		fmt.Println(hostd.Status())
	case "feedback-mcp":
		err = feedbackMCP(*data)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "solco-hostd:", err)
		os.Exit(1)
	}
}

func run(dataDir string) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("solco-hostd started", "version", version)
	fdir := feedback.Dir(dataDir)
	go func() {
		_ = feedback.Run(ctx, feedback.Options{Dir: fdir, Version: version, Notifier: feedback.DefaultNotifier(fdir),
			ImportLegacy: feedback.LegacyDecisionsPath(), Log: log})
	}()
	return hostd.Run(ctx, hostd.Options{
		Dir: hostd.RemoteDir(dataDir),
		Settings: func() settings.RemoteAccessSettings {
			r, err := hostd.LoadRemoteSettings(dataDir)
			if err != nil {
				return settings.RemoteAccessSettings{} // unreadable = off, never wider
			}
			return r
		},
		New: hostd.NewServer(dataDir, version),
		Log: log,
	})
}

// feedbackMCP is what an Agent's MCP configuration launches. Stdout belongs to the protocol, so every log goes to
// stderr.
func feedbackMCP(dataDir string) error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dir := feedback.Dir(dataDir)
	return mcpshim.Run(ctx, dir, version, feedback.DefaultNotifier(dir))
}
