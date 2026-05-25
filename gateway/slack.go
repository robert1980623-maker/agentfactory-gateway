package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/agentfactory/gateway/protocol"
	statemgr "github.com/agentfactory/gateway/state"
	"github.com/agentfactory/gateway/worker"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type SlackGateway struct {
	client       *slack.Client
	sm           *socketmode.Client
	worker       *worker.PythonWorker
	streamWorker *worker.StreamWorker
	stateMgr     *statemgr.StateManager
	hitlHandler  *HITLHandler
}

func NewSlackGateway(botToken, appToken string, w *worker.PythonWorker, sw *worker.StreamWorker, stateMgr *statemgr.StateManager) *SlackGateway {
	client := slack.New(botToken, slack.OptionAppLevelToken(appToken))
	socketmodeClient := socketmode.New(client)
	hitlHandler := NewHITLHandler(w, sw, stateMgr, client)
	return &SlackGateway{
		client:       client,
		sm:           socketmodeClient,
		worker:       w,
		streamWorker: sw,
		stateMgr:     stateMgr,
		hitlHandler:  hitlHandler,
	}
}

func (g *SlackGateway) Start(ctx context.Context) error {
	go func() {
		for evt := range g.sm.Events {
			switch evt.Type {
			case socketmode.EventTypeEventsAPI:
				g.handleEvent(evt)
			case socketmode.EventTypeInteractive:
				g.handleInteractiveCallback(evt)
			case socketmode.EventTypeConnecting:
				log.Println("Connecting to Slack with Socket Mode...")
			case socketmode.EventTypeConnectionError:
				log.Printf("Connection failed: %v", evt.Data)
			case socketmode.EventTypeConnected:
				log.Println("Connected to Slack with Socket Mode.")
			default:
				g.sm.Ack(*evt.Request)
			}
		}
	}()

	err := g.sm.RunContext(ctx)

	// Context was cancelled — perform graceful drain.
	g.Stop(context.Background())
	return err
}

// Stop gracefully shuts down the gateway, draining active workers and
// marking interrupted tasks in the state manager.
func (g *SlackGateway) Stop(ctx context.Context) error {
	log.Println("Draining active workers...")

	// Drain stream worker.
	if g.streamWorker != nil {
		g.streamWorker.Stop()
	}

	// Drain Python worker.
	if g.worker != nil {
		g.worker.Stop()
	}

	// Mark all running tasks as interrupted.
	if g.stateMgr != nil {
		active := g.stateMgr.ListActive()
		for _, rec := range active {
			log.Printf("Marking task %s as interrupted", rec.TaskID)
			if err := g.stateMgr.Set(statemgr.TaskRecord{
				TaskID: rec.TaskID,
				Status: "interrupted",
			}); err != nil {
				log.Printf("Failed to mark task %s as interrupted: %v", rec.TaskID, err)
			}
		}
	}

	return nil
}

func (g *SlackGateway) handleEvent(evt socketmode.Event) {
	eventsAPI, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		log.Printf("Failed to convert event data: %T", evt.Data)
		return
	}

	g.sm.Ack(*evt.Request)

	if mentionEvent, ok := eventsAPI.InnerEvent.Data.(*slackevents.AppMentionEvent); ok {
		g.handleMentionStream(mentionEvent)
	}
}

func (g *SlackGateway) handleInteractiveCallback(evt socketmode.Event) {
	g.sm.Ack(*evt.Request)

	var callback slack.InteractionCallback
	if err := json.Unmarshal(evt.Data.([]byte), &callback); err != nil {
		log.Printf("Failed to parse interaction callback: %v", err)
		return
	}

	// Handle view submissions (modal).
	if callback.Type == slack.InteractionTypeViewSubmission {
		if g.hitlHandler != nil {
			g.hitlHandler.HandleViewSubmission(callback)
		}
		return
	}

	// Try HITL handler first for block actions.
	if g.hitlHandler != nil && g.hitlHandler.HandleAction(callback) {
		return
	}

	if len(callback.ActionCallback.BlockActions) == 0 {
		return
	}

	action := callback.ActionCallback.BlockActions[0]
	log.Printf("Interactive callback: action=%s user=%s channel=%s", action.Value, callback.User.ID, callback.Channel.ID)

	switch action.Value {
	case "stop_task":
		g.handleStopTask(callback)
	case "retry_task":
		g.handleRetryTask(callback)
	case "copy_code":
		// No-op: client-side handles this via Slack's native copy.
		log.Println("copy_code action acknowledged (client-side handled)")
	}
}

func (g *SlackGateway) handleStopTask(callback slack.InteractionCallback) {
	if g.stateMgr == nil {
		log.Println("stop_task: stateMgr is nil, skipping")
		g.reply(callback.Channel.ID, "Task stop requested, but state manager is unavailable.")
		return
	}

	// Find the running task for this channel.
	active := g.stateMgr.ListActive()
	for _, rec := range active {
		if rec.ChannelID == callback.Channel.ID {
			if err := g.stateMgr.Set(statemgr.TaskRecord{
				TaskID: rec.TaskID,
				Status: "stopped",
			}); err != nil {
				log.Printf("Failed to mark task as stopped: %v", err)
				g.reply(callback.Channel.ID, "Failed to stop the task.")
				return
			}
			g.reply(callback.Channel.ID, fmt.Sprintf("Task %s has been stopped.", rec.TaskID))
			return
		}
	}

	g.reply(callback.Channel.ID, "No active task found to stop.")
}

func (g *SlackGateway) handleRetryTask(callback slack.InteractionCallback) {
	if g.stateMgr == nil {
		log.Println("retry_task: stateMgr is nil, skipping")
		g.reply(callback.Channel.ID, "Task retry requested, but state manager is unavailable.")
		return
	}

	// Find the last task for this channel to get its original input.
	// We look for any record (done, error, stopped) for this channel.
	active := g.stateMgr.ListActive()
	for _, rec := range active {
		if rec.ChannelID == callback.Channel.ID {
			// Re-run the same task by creating a synthetic mention event.
			mentionEvent := &slackevents.AppMentionEvent{
				User:      callback.User.ID,
				Text:      "<@BOT_ID> retry last task",
				Channel:   callback.Channel.ID,
				TimeStamp: callback.MessageTs,
			}
			g.handleMentionStream(mentionEvent)
			return
		}
	}

	g.reply(callback.Channel.ID, "No task found to retry.")
}

// taskState holds the Slack message reference for an ongoing streaming task.
type taskState struct {
	channelID string
	ts        string
	taskData  *TaskData
}

func (g *SlackGateway) handleMentionStream(event *slackevents.AppMentionEvent) {
	log.Printf("App mention (stream): user=%s text=%s channel=%s", event.User, event.Text, event.Channel)

	// Prevent concurrent task execution in the same channel.
	if g.stateMgr != nil && g.stateMgr.HasActiveTask(event.Channel) {
		log.Printf("Channel %s already has an active task, rejecting", event.Channel)
		g.reply(event.Channel, "⏳ A task is already running in this channel. Please wait for it to complete or use /stop.")
		return
	}

	req := protocol.TaskRequest{
		Task: event.Text,
	}

	ts := &taskState{
		channelID: event.Channel,
		taskData:  &TaskData{},
	}

	cb := func(evt *protocol.SlackEvent, err error) {
		if err != nil {
			log.Printf("Stream callback error: %v", err)
			g.postError(ts, err.Error())
			return
		}
		if evt == nil {
			return
		}

		blocks := BuildTaskCard(evt, ts.taskData)
		if blocks == nil {
			return
		}

		// Update taskData metadata from the event.
		if evt.Payload != nil {
			if evt.Payload.Model != "" {
				ts.taskData.Model = evt.Payload.Model
			}
			if evt.Payload.Tokens > 0 {
				ts.taskData.Tokens = evt.Payload.Tokens
			}
		}

		switch evt.Type {
		case protocol.EventTypeStart:
			g.postMessage(ts, blocks)
			// Capture task_id from the event and set state to running.
			if evt.Payload != nil && evt.Payload.TaskID != "" && g.stateMgr != nil {
				if err := g.stateMgr.Set(statemgr.TaskRecord{
					TaskID:    evt.Payload.TaskID,
					ChannelID: ts.channelID,
					SlackTS:   ts.ts,
					UserID:    event.User,
					Prompt:    event.Text,
					Status:    "running",
				}); err != nil {
					log.Printf("Failed to set task state: %v", err)
				}
			}
		case protocol.EventTypeProgress:
			g.updateMessage(ts, blocks)
			// Update state timestamp (use Set to touch UpdatedAt).
			if evt.Payload != nil && evt.Payload.TaskID != "" && g.stateMgr != nil {
				if err := g.stateMgr.Set(statemgr.TaskRecord{
					TaskID: evt.Payload.TaskID,
					Status: "running",
				}); err != nil {
					log.Printf("Failed to update task state: %v", err)
				}
			}
		case protocol.EventTypePaused:
			g.updateMessage(ts, blocks)
			// Mark task as paused in state manager.
			if evt.Payload != nil && evt.Payload.TaskID != "" && g.stateMgr != nil {
				if err := g.stateMgr.Set(statemgr.TaskRecord{
					TaskID:    evt.Payload.TaskID,
					ChannelID: ts.channelID,
					SlackTS:   ts.ts,
					Status:    "paused",
				}); err != nil {
					log.Printf("Failed to set task paused state: %v", err)
				}
			}
		case protocol.EventTypeDone:
			g.updateMessage(ts, blocks)
			// Mark task as done.
			if evt.Payload != nil && evt.Payload.TaskID != "" && g.stateMgr != nil {
				if err := g.stateMgr.Set(statemgr.TaskRecord{
					TaskID:    evt.Payload.TaskID,
					ChannelID: ts.channelID,
					SlackTS:   ts.ts,
					Status:    "done",
				}); err != nil {
					log.Printf("Failed to set task state: %v", err)
				}
			}
		case protocol.EventTypeError:
			g.updateMessage(ts, blocks)
			// Mark task as error.
			if evt.Payload != nil && evt.Payload.TaskID != "" && g.stateMgr != nil {
				if err := g.stateMgr.Set(statemgr.TaskRecord{
					TaskID:    evt.Payload.TaskID,
					ChannelID: ts.channelID,
					SlackTS:   ts.ts,
					Status:    "error",
				}); err != nil {
					log.Printf("Failed to set task state: %v", err)
				}
			}
		}
	}

	if g.streamWorker != nil {
		if err := g.streamWorker.Execute(req, cb); err != nil {
			log.Printf("Stream worker error: %v", err)
			g.postError(ts, err.Error())
		}
	} else {
		// Fallback to non-streaming worker.
		resp, err := g.worker.Execute(req)
		if err != nil {
			log.Printf("Worker error: %v", err)
			g.reply(event.Channel, "Sorry, an error occurred: "+err.Error())
			return
		}
		if resp.Error != "" {
			g.reply(event.Channel, "Task failed: "+resp.Error)
			return
		}
		g.reply(event.Channel, resp.Output)
	}
}

func (g *SlackGateway) postMessage(ts *taskState, blocks []slack.Block) {
	opts := []slack.MsgOption{slack.MsgOptionBlocks(blocks...)}
	_, msgTS, err := g.client.PostMessage(ts.channelID, opts...)
	if err != nil {
		log.Printf("Failed to post message: %v", err)
		return
	}
	ts.ts = msgTS
}

func (g *SlackGateway) updateMessage(ts *taskState, blocks []slack.Block) {
	if ts.ts == "" {
		// Fallback: post a new message if no ts is stored.
		g.postMessage(ts, blocks)
		return
	}

	opts := []slack.MsgOption{
		slack.MsgOptionBlocks(blocks...),
	}
	_, _, _, err := g.client.UpdateMessage(ts.channelID, ts.ts, opts...)
	if err != nil {
		log.Printf("Failed to update message: %v", err)
	}
}

func (g *SlackGateway) postError(ts *taskState, msg string) {
	event := &protocol.SlackEvent{
		Type: protocol.EventTypeError,
		Payload: &protocol.EventPayload{
			Message: msg,
		},
	}
	blocks := BuildTaskCard(event, ts.taskData)
	if ts.ts == "" {
		g.postMessage(ts, blocks)
	} else {
		g.updateMessage(ts, blocks)
	}
}

func (g *SlackGateway) reply(channel, text string) {
	_, _, err := g.client.PostMessage(channel, slack.MsgOptionText(text, false))
	if err != nil {
		log.Printf("Failed to reply: %v", err)
	}
}
