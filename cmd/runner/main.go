package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/slack-go/slack"
)

const logDir = "logs"

// Reactions that mean "already in flight or finished". Runner only reads.
const (
	reactionRunning = "hourglass_flowing_sand"
	reactionDone    = "white_check_mark"
	reactionFailed  = "x"
)

func main() {
	if err := run(); err != nil {
		slog.Error("runner exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	api := slack.New(cfg.BotToken)

	hasWork, err := pendingExists(api, cfg.TaskChannel)
	if err != nil {
		return fmt.Errorf("check pending: %w", err)
	}
	if !hasWork {
		slog.Info("no pending work")
		return nil
	}

	logPath, err := newLogPath()
	if err != nil {
		return fmt.Errorf("prepare log: %w", err)
	}
	slog.Info("pending work found, launching command (detached)", "log", logPath)
	return launchDetached(cfg.Command, logPath)
}

// pendingExists returns true when the most recent message in the channel
// has none of the {running, done, failed} reactions.
func pendingExists(api *slack.Client, channel string) (bool, error) {
	history, err := api.GetConversationHistory(&slack.GetConversationHistoryParameters{
		ChannelID: channel,
		Limit:     1,
	})
	if err != nil {
		return false, err
	}
	if len(history.Messages) == 0 {
		return false, nil
	}
	handled := []string{reactionRunning, reactionDone, reactionFailed}
	for _, r := range history.Messages[0].Reactions {
		if slices.Contains(handled, r.Name) {
			return false, nil
		}
	}
	return true, nil
}

func newLogPath() (string, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", err
	}
	ts := time.Now().Format("2006-01-02_15-04-05")
	return filepath.Join(logDir, ts+".log"), nil
}

func launchDetached(command, logPath string) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	// Detach stdin: this function returns immediately. Sharing our fd
	// would give the command SIGPIPE on its next read.
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull

	// Pipe stdout/stderr into the log file. The command keeps its dup of
	// the fd after we close ours, so writes survive runner exit.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	slog.Info("command detached", "pid", cmd.Process.Pid)
	return cmd.Process.Release()
}

type config struct {
	BotToken    string
	TaskChannel string
	Command     string
}

func loadConfig() (config, error) {
	var missing []string
	cfg := config{
		BotToken:    os.Getenv("SLACK_BOT_TOKEN"),
		TaskChannel: os.Getenv("SLACK_TASK_CHANNEL"),
		Command:     os.Getenv("COMMAND"),
	}
	if cfg.BotToken == "" {
		missing = append(missing, "SLACK_BOT_TOKEN")
	}
	if cfg.TaskChannel == "" {
		missing = append(missing, "SLACK_TASK_CHANNEL")
	}
	if cfg.Command == "" {
		missing = append(missing, "COMMAND")
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}
