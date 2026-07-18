// Package slack implements a Socket Mode HelpChannel adapter: agent
// questions are posted to a configured Slack channel (with task/run context
// and option buttons), and replies — whether typed into the thread or a
// button click — flow back through the daemon's ReplySink.
//
// Socket Mode requires no inbound public endpoint, so this fits a
// laptop/self-hosted daemon: it opens an outbound WebSocket via
// apps.connections.open using an app-level token, and posts/reads messages
// over the Web API using a bot token.
package slack

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
)

// externalChannelName is the HelpStore binding namespace this adapter uses
// (the "channel_name" column in help_bindings), distinct from the Slack
// channel a thread is posted to.
const externalChannelName = "slack"

// Binder is the subset of ports.HelpStore this adapter needs to persist and
// resolve the mapping between cloche thread IDs and Slack thread_ts values.
type Binder interface {
	BindExternal(ctx context.Context, threadID, channelName, externalID string) error
	ResolveExternal(ctx context.Context, channelName, externalID string) (threadID string, err error)
	GetExternalID(ctx context.Context, threadID, channelName string) (externalID string, err error)
}

// Config configures the Slack HelpChannel adapter. Mirrors the
// `[[help.channel]]` (type = "slack") entries in the daemon config.
type Config struct {
	// Channel is the default Slack channel messages are posted to (e.g. "#cloche").
	Channel string
	// TokenEnv/AppTokenEnv name the env vars holding the bot token (xoxb-...)
	// and app-level token (xapp-...) respectively.
	TokenEnv    string
	AppTokenEnv string
	// ChannelMap optionally routes distinct cloche channels (project names)
	// to distinct Slack channels; cloche channels absent from the map fall
	// back to Channel.
	ChannelMap map[string]string
}

// Channel is a Socket Mode HelpChannel adapter for Slack.
type Channel struct {
	cfg    Config
	binder Binder
	api    *slack.Client
	sm     *socketmode.Client

	botUserID string
}

// New validates cfg and constructs a Slack Channel. It does not connect;
// connecting happens in Start. Returns an error if the configured token env
// vars are unset — callers should log and skip this channel rather than
// treat that as fatal (the CLI channel always works regardless).
func New(cfg Config, binder Binder) (*Channel, error) {
	if cfg.TokenEnv == "" {
		cfg.TokenEnv = "SLACK_BOT_TOKEN"
	}
	if cfg.AppTokenEnv == "" {
		cfg.AppTokenEnv = "SLACK_APP_TOKEN"
	}
	if cfg.Channel == "" {
		cfg.Channel = "#cloche"
	}

	botToken := os.Getenv(cfg.TokenEnv)
	if botToken == "" {
		return nil, fmt.Errorf("slack: env var %s is not set", cfg.TokenEnv)
	}
	appToken := os.Getenv(cfg.AppTokenEnv)
	if appToken == "" {
		return nil, fmt.Errorf("slack: env var %s is not set", cfg.AppTokenEnv)
	}

	api := slack.New(botToken, slack.OptionAppLevelToken(appToken))
	sm := socketmode.New(api)

	return &Channel{cfg: cfg, binder: binder, api: api, sm: sm}, nil
}

// Name implements ports.HelpChannel.
func (c *Channel) Name() string { return externalChannelName }

// Start implements ports.HelpChannel. It runs the Socket Mode connection
// until ctx is cancelled, dispatching bound-thread replies and option-button
// clicks to sink.
func (c *Channel) Start(ctx context.Context, sink ports.ReplySink) error {
	auth, err := c.api.AuthTestContext(ctx)
	if err != nil {
		return fmt.Errorf("slack: auth test failed (check %s/%s): %w", c.cfg.TokenEnv, c.cfg.AppTokenEnv, err)
	}
	c.botUserID = auth.UserID

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-c.sm.Events:
				if !ok {
					return
				}
				c.handleEvent(ctx, evt, sink)
			}
		}
	}()

	if err := c.sm.RunContext(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("slack: socket mode connection stopped: %w", err)
	}
	return nil
}

// Post implements ports.HelpChannel. The first message of a thread posts to
// the configured Slack channel with task/run context and option buttons;
// the Slack thread_ts is bound so follow-ups post into the same thread.
func (c *Channel) Post(ctx context.Context, thread domain.HelpThread, msg domain.HelpMessage) error {
	existingTs, err := c.binder.GetExternalID(ctx, thread.ID, externalChannelName)
	if err != nil {
		return fmt.Errorf("slack: resolve thread binding: %w", err)
	}

	slackChannel := c.cfg.Channel
	if mapped, ok := c.cfg.ChannelMap[thread.Channel]; ok && mapped != "" {
		slackChannel = mapped
	}

	opts := []slack.MsgOption{
		slack.MsgOptionBlocks(buildBlocks(thread, msg, existingTs == "")...),
		slack.MsgOptionText(fallbackText(thread, msg), false),
	}
	if existingTs != "" {
		opts = append(opts, slack.MsgOptionTS(existingTs))
	}

	_, ts, err := c.api.PostMessageContext(ctx, slackChannel, opts...)
	if err != nil {
		return fmt.Errorf("slack: post message to %s: %w", slackChannel, err)
	}

	if existingTs == "" {
		if err := c.binder.BindExternal(ctx, thread.ID, externalChannelName, ts); err != nil {
			return fmt.Errorf("slack: bind thread %s to %s: %w", thread.ID, ts, err)
		}
	}
	return nil
}

func (c *Channel) handleEvent(ctx context.Context, evt socketmode.Event, sink ports.ReplySink) {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		apiEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		if evt.Request != nil {
			c.sm.Ack(*evt.Request)
		}
		if apiEvent.Type != slackevents.CallbackEvent {
			return
		}
		if msgEvent, ok := apiEvent.InnerEvent.Data.(*slackevents.MessageEvent); ok {
			c.handleMessage(ctx, msgEvent, sink)
		}

	case socketmode.EventTypeInteractive:
		cb, ok := evt.Data.(slack.InteractionCallback)
		if !ok {
			return
		}
		if evt.Request != nil {
			c.sm.Ack(*evt.Request)
		}
		if cb.Type == slack.InteractionTypeBlockActions {
			c.handleBlockAction(ctx, cb, sink)
		}
	}
}

// handleMessage resolves a plain-text reply typed into a bound Slack thread
// and feeds it to sink.Reply.
func (c *Channel) handleMessage(ctx context.Context, ev *slackevents.MessageEvent, sink ports.ReplySink) {
	if ev.BotID != "" || ev.SubType != "" || (c.botUserID != "" && ev.User == c.botUserID) {
		return // ignore our own posts and edits/deletes/other subtypes
	}
	if ev.ThreadTimeStamp == "" || ev.ThreadTimeStamp == ev.TimeStamp {
		return // not a reply within a thread
	}

	threadID, err := c.binder.ResolveExternal(ctx, externalChannelName, ev.ThreadTimeStamp)
	if err != nil {
		log.Printf("help/slack: resolve external %s: %v", ev.ThreadTimeStamp, err)
		return
	}
	if threadID == "" {
		return // not a cloche-bound thread
	}

	if err := sink.Reply(ctx, threadID, ev.Text, externalChannelName); err != nil {
		log.Printf("help/slack: reply to thread %s: %v", threadID, err)
	}
}

// handleBlockAction resolves an option-button click in a bound Slack thread
// and replies with the option's text.
func (c *Channel) handleBlockAction(ctx context.Context, cb slack.InteractionCallback, sink ports.ReplySink) {
	if len(cb.ActionCallback.BlockActions) == 0 {
		return
	}

	threadTs := cb.Container.ThreadTs
	if threadTs == "" {
		threadTs = cb.Container.MessageTs
	}
	if threadTs == "" {
		return
	}

	threadID, err := c.binder.ResolveExternal(ctx, externalChannelName, threadTs)
	if err != nil {
		log.Printf("help/slack: resolve external %s: %v", threadTs, err)
		return
	}
	if threadID == "" {
		return
	}

	action := cb.ActionCallback.BlockActions[0]
	option := action.Value
	if option == "" {
		option = action.Text.Text
	}

	if err := sink.Reply(ctx, threadID, option, externalChannelName); err != nil {
		log.Printf("help/slack: reply to thread %s: %v", threadID, err)
	}
}

// buildBlocks renders a HelpMessage as Slack blocks. The first message of a
// thread includes a task/run context line; follow-ups are just the message
// body. Both include option buttons when the message has any.
func buildBlocks(thread domain.HelpThread, msg domain.HelpMessage, isFirst bool) []slack.Block {
	var blocks []slack.Block

	if isFirst {
		header := fmt.Sprintf(":question: *New question from cloche* — %s", thread.Title)
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, header, false, false), nil, nil))

		var ctxParts []string
		if thread.TaskID != "" {
			ctxParts = append(ctxParts, "Task: "+thread.TaskID)
		}
		if thread.RunID != "" {
			ctxParts = append(ctxParts, "Run: "+thread.RunID)
		}
		if thread.StepName != "" {
			ctxParts = append(ctxParts, "Step: "+thread.StepName)
		}
		ctxParts = append(ctxParts, "Address: "+thread.Address())
		blocks = append(blocks, slack.NewContextBlock("",
			slack.NewTextBlockObject(slack.MarkdownType, strings.Join(ctxParts, "  •  "), false, false)))
	}

	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, msg.Body, false, false), nil, nil))

	if len(msg.Options) > 0 {
		var elements []slack.BlockElement
		for i, opt := range msg.Options {
			elements = append(elements, slack.NewButtonBlockElement(
				fmt.Sprintf("option_%d", i), opt,
				slack.NewTextBlockObject(slack.PlainTextType, opt, true, false)))
		}
		blocks = append(blocks, slack.NewActionBlock("", elements...))
	}

	return blocks
}

// fallbackText is the plain-text summary Slack shows in notifications/search
// where blocks aren't rendered.
func fallbackText(thread domain.HelpThread, msg domain.HelpMessage) string {
	if thread.Title != "" {
		return thread.Title + ": " + msg.Body
	}
	return msg.Body
}
