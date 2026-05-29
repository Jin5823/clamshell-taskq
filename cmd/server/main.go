package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(
		context.Background(), syscall.SIGINT, syscall.SIGTERM,
	)
	defer stop()

	api := slack.New(cfg.BotToken, slack.OptionAppLevelToken(cfg.AppToken))
	client := socketmode.New(api)

	slog.Info("server starting",
		"mode", "socket",
		"task_channel", cfg.TaskChannel,
	)

	go consumeEvents(client, api, cfg.TaskChannel)

	if err := client.RunContext(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("socket mode: %w", err)
	}

	slog.Info("server stopped", "cause", context.Cause(ctx))
	return nil
}

// consumeEvents drains the socketmode event stream until the client closes
// it (which happens when ctx passed to RunContext is canceled).
func consumeEvents(client *socketmode.Client, api *slack.Client, taskChannel string) {
	for evt := range client.Events {
		switch evt.Type {
		case socketmode.EventTypeConnecting:
			slog.Info("socket mode connecting")
		case socketmode.EventTypeConnected:
			slog.Info("socket mode connected")
		case socketmode.EventTypeDisconnect:
			slog.Info("socket mode disconnect")
		case socketmode.EventTypeEventsAPI:
			apiEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
			if !ok {
				continue
			}
			client.Ack(*evt.Request)
			handOff(api, taskChannel, apiEvent)
		}
	}
}

func handOff(api *slack.Client, taskChannel string, event slackevents.EventsAPIEvent) {
	if event.Type != slackevents.CallbackEvent {
		return
	}
	mention, ok := event.InnerEvent.Data.(*slackevents.AppMentionEvent)
	if !ok {
		return
	}
	if mention.Channel == taskChannel {
		return
	}
	text := formatMention(mention)
	if _, _, err := api.PostMessage(taskChannel, slack.MsgOptionText(text, false)); err != nil {
		slog.Error("queue push failed",
			"ts", mention.TimeStamp, "user", mention.User, "error", err,
		)
		return
	}
	slog.Info("queue push ok", "ts", mention.TimeStamp, "user", mention.User)
}

func formatMention(m *slackevents.AppMentionEvent) string {
	return fmt.Sprintf(
		"[source:slack] mention from <@%s> in <#%s>\ntext:\n%s\nsource-ts: %s",
		m.User, m.Channel, m.Text, m.TimeStamp,
	)
}

type config struct {
	BotToken    string
	AppToken    string
	TaskChannel string
}

func loadConfig() (config, error) {
	var missing []string
	cfg := config{
		BotToken:    os.Getenv("SLACK_BOT_TOKEN"),
		AppToken:    os.Getenv("SLACK_APP_TOKEN"),
		TaskChannel: os.Getenv("SLACK_TASK_CHANNEL"),
	}
	if cfg.BotToken == "" {
		missing = append(missing, "SLACK_BOT_TOKEN")
	}
	if cfg.AppToken == "" {
		missing = append(missing, "SLACK_APP_TOKEN")
	}
	if cfg.TaskChannel == "" {
		missing = append(missing, "SLACK_TASK_CHANNEL")
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}
