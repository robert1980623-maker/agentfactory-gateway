package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentfactory/gateway/protocol"
	statemgr "github.com/agentfactory/gateway/state"
	"github.com/agentfactory/gateway/worker"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// progressCache debounces per-task SQLite writes for progress events.
type progressCache struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newProgressCache() *progressCache { return &progressCache{last: make(map[string]time.Time)} }

func (pc *progressCache) shouldPersist(taskID string, cooldown time.Duration) bool {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	last, ok := pc.last[taskID]
	if !ok || time.Since(last) >= cooldown {
		pc.last[taskID] = time.Now().UTC()
		return true
	}
	return false
}

type SlackGateway struct {
	client        *slack.Client
	sm            *socketmode.Client
	worker        *worker.PythonWorker
	streamWorker  *worker.StreamWorker
	stateMgr      statemgr.StateManager
	taskQueue     *TaskQueue
	hitlHandler   *HITLHandler
	progressCache *progressCache

	mu      sync.Mutex
	stopped bool
}

func NewSlackGateway(botToken, appToken string, w *worker.PythonWorker, sw *worker.StreamWorker, stateMgr statemgr.StateManager) *SlackGateway {
	client := slack.New(botToken, slack.OptionAppLevelToken(appToken))
	socketmodeClient := socketmode.New(client)
	hitlHandler := NewHITLHandler(w, sw, stateMgr, client)
	taskQueue := NewTaskQueue(TaskQueueConfig{MaxConcurrentTasks: 5, MaxPerChannel: 1})
	g := &SlackGateway{
		client:        client,
		sm:            socketmodeClient,
		worker:        w,
		streamWorker:  sw,
		stateMgr:      stateMgr,
		taskQueue:     taskQueue,
		hitlHandler:   hitlHandler,
		progressCache: newProgressCache(),
	}

	// Set dequeue callback to automatically dispatch the next queued task.
	taskQueue.SetDequeueCallback(func(task *QueuedTask) {
		g.dispatchQueuedTask(task)
	})

	return g
}

// dispatchQueuedTask dispatches a dequeued task to the stream worker.
func (g *SlackGateway) dispatchQueuedTask(task *QueuedTask) {
	log.Printf("Dispatching queued task %s (channel=%s, pos=%d)", task.TaskID, task.ChannelID, g.taskQueue.Position(task.TaskID))

	ts := &taskState{
		channelID: task.ChannelID,
		taskData:  &TaskData{},
	}

	cb := func(evt *protocol.SlackEvent, err error) {
		if err != nil {
			atomic.AddInt64(&metricErrors, 1)
			log.Printf("Stream callback error for queued task %s: %v", task.TaskID, err)
			g.postError(ts, err.Error())
			g.taskQueue.MarkError(task.TaskID)
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
			// Mark task as running in the queue (was Dispatching after dequeue).
			g.taskQueue.MarkDispatched(task.TaskID)
			// Capture task_id from the event and set state to running.
			if evt.Payload != nil && evt.Payload.TaskID != "" && g.stateMgr != nil {
				if err := g.stateMgr.Set(statemgr.TaskRecord{
					TaskID:    evt.Payload.TaskID,
					ChannelID: ts.channelID,
					SlackTS:   ts.ts,
					UserID:    task.UserID,
					Prompt:    task.Prompt,
					Status:    "running",
				}); err != nil {
					log.Printf("Failed to set task state: %v", err)
				}
			}
		case protocol.EventTypeProgress:
			g.updateMessage(ts, blocks)
			// Update state timestamp (use Set to touch UpdatedAt).
			// Debounced: only persist every 10s per task to avoid SQLite spam.
			if evt.Payload != nil && evt.Payload.TaskID != "" && g.stateMgr != nil && g.progressCache.shouldPersist(evt.Payload.TaskID, 10*time.Second) {
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
			// Mark task as done in state manager and queue.
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
			g.taskQueue.MarkDone(task.TaskID)
		case protocol.EventTypeError:
			g.updateMessage(ts, blocks)
			// Mark task as error in state manager and queue.
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
			g.taskQueue.MarkError(task.TaskID)
		}
	}

	req := protocol.TaskRequest{Task: task.Prompt}
	if g.streamWorker != nil {
		if err := g.streamWorker.Execute(req, cb); err != nil {
			atomic.AddInt64(&metricErrors, 1)
			log.Printf("Stream worker error for queued task %s: %v", task.TaskID, err)
			g.postError(ts, err.Error())
			g.taskQueue.MarkError(task.TaskID)
		}
	}
}

// ReconcileAfterRecovery synchronises the in-memory TaskQueue with the results
// of a crash recovery run. It must be called after the Gateway has been
// created (so the taskQueue exists) but before the main event loop starts.
//
// For each recovered task:
//   - terminal status (done / error / stopped) → release the channel slot and
//     trigger tryDequeue for any waiting tasks
//   - still running → MarkRunningDirect to register the task in the queue
func (g *SlackGateway) ReconcileAfterRecovery(results []RecoveryResult) {
	for _, r := range results {
		switch r.FinalStatus {
		case "done", "error", "stopped":
			// Task has reached a terminal state. Add to running map so that
			// we hold the channel slot, then remove it (decrementing the
			// channel counter) and trigger tryDequeue for any waiting tasks.
			qt := &QueuedTask{
				TaskID:    r.TaskID,
				ChannelID: r.ChannelID,
				Status:    TaskStatusRunning,
				CreatedAt: time.Now().UTC(),
			}
			g.taskQueue.MarkRunningDirect(qt)
			g.taskQueue.releaseTerminalTask(r.TaskID, r.FinalStatus == "done")
		case "running":
			// Task is genuinely still executing; track it in the queue.
			qt := &QueuedTask{
				TaskID:    r.TaskID,
				ChannelID: r.ChannelID,
				Status:    TaskStatusRunning,
				CreatedAt: time.Now().UTC(),
			}
			g.taskQueue.MarkRunningDirect(qt)
		default:
			// Unknown terminal status — treat as error.
			qt := &QueuedTask{
				TaskID:    r.TaskID,
				ChannelID: r.ChannelID,
				Status:    TaskStatusRunning,
				CreatedAt: time.Now().UTC(),
			}
			g.taskQueue.MarkRunningDirect(qt)
			g.taskQueue.releaseTerminalTask(r.TaskID, false)
		}
	}
}

func (g *SlackGateway) Start(ctx context.Context) error {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[PANIC] Event handler goroutine panicked: %v", r)
			}
		}()
		for evt := range g.sm.Events {
			log.Printf("[SOCKETMODE EVENT] Type=%v (data=%T)", evt.Type, evt.Data)
			switch evt.Type {
			case socketmode.EventTypeEventsAPI:
				g.handleEvent(evt)
			case socketmode.EventTypeInteractive:
				g.handleInteractiveCallback(evt)
			case socketmode.EventTypeConnecting:
				log.Println("[SOCKETMODE] Connecting...")
			case socketmode.EventTypeConnectionError:
				log.Printf("[SOCKETMODE] Connection failed: %v", evt.Data)
			case socketmode.EventTypeConnected:
				log.Println("[SOCKETMODE] Connected.")
			default:
				log.Printf("[SOCKETMODE] Unhandled event type: %v", evt.Type)
				if evt.Request != nil {
					g.sm.Ack(*evt.Request)
				}
			}
		}
	}()

	log.Println("[GATEWAY] Calling RunContext...")
	err := g.sm.RunContext(ctx)
	log.Printf("[GATEWAY] RunContext returned: %v", err)

	// Context was cancelled — perform graceful drain.
	g.Stop(context.Background())
	return err
}

// Stop gracefully shuts down the gateway, draining active workers and
// marking interrupted tasks in the state manager. It is idempotent:
// calling Stop multiple times is safe and will only perform the drain once.
func (g *SlackGateway) Stop(ctx context.Context) error {
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		log.Println("Stop called but gateway already stopped, skipping.")
		return nil
	}
	g.stopped = true
	g.mu.Unlock()

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
	atomic.AddInt64(&totalEventsProcessed, 1)
	log.Printf("[EVENT RECEIVED] Type: %v, Data type: %T", evt.Type, evt.Data)

	eventsAPI, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		log.Printf("[EVENT] Failed to convert event data: %T", evt.Data)
		return
	}
	log.Printf("[EVENT] Type: %s, InnerEvent type: %T", eventsAPI.Type, eventsAPI.InnerEvent.Data)
	log.Printf("[EVENT] InnerEvent data: %+v", eventsAPI.InnerEvent.Data)

	g.sm.Ack(*evt.Request)

	if mentionEvent, ok := eventsAPI.InnerEvent.Data.(*slackevents.AppMentionEvent); ok {
		log.Printf("[EVENT] AppMentionEvent matched: user=%s, text=%q, channel=%s", mentionEvent.User, mentionEvent.Text, mentionEvent.Channel)
		g.handleMentionStream(mentionEvent)
		return
	}

	// Handle direct messages (DMs).
	if msgEvent, ok := eventsAPI.InnerEvent.Data.(*slackevents.MessageEvent); ok {
		log.Printf("[EVENT] MessageEvent matched: user=%s, text=%q, channel=%s, subtype=%q", msgEvent.User, msgEvent.Text, msgEvent.Channel, msgEvent.SubType)
		// Ignore bot messages and edited messages.
		if msgEvent.SubType == "bot_message" || msgEvent.SubType == "message_changed" {
			log.Printf("[EVENT] Ignoring message with subtype: %s", msgEvent.SubType)
			return
		}
		// Convert DM message to AppMentionEvent format for unified handling.
		mentionEvent := &slackevents.AppMentionEvent{
			User:      msgEvent.User,
			Text:      msgEvent.Text,
			Channel:   msgEvent.Channel,
			TimeStamp: msgEvent.TimeStamp,
		}
		log.Printf("[EVENT] DM converted to mention: user=%s, text=%q, channel=%s", mentionEvent.User, mentionEvent.Text, mentionEvent.Channel)
		g.handleMentionStream(mentionEvent)
		return
	}

	log.Printf("[EVENT] Unrecognized inner event type: %T — data: %+v", eventsAPI.InnerEvent.Data, eventsAPI.InnerEvent.Data)
}

func (g *SlackGateway) handleInteractiveCallback(evt socketmode.Event) {
	atomic.AddInt64(&totalEventsProcessed, 1)
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

	// Check if this channel already has an active (running or queued) task.
	if g.taskQueue != nil && g.taskQueue.ChannelHasActiveTask(event.Channel) {
		log.Printf("Channel %s already has an active task, queuing", event.Channel)

		queuedTask := NewQueuedTask(
			g.generateTaskID(),
			event.Channel,
			event.User,
			event.Text,
		)

		pos, err := g.taskQueue.Enqueue(queuedTask)
		if err != nil {
			log.Printf("Failed to enqueue task: %v", err)
			g.reply(event.Channel, "Failed to queue your task. Please try again.")
			return
		}

		// Post a queued status card.
		ts := &taskState{channelID: event.Channel, taskData: &TaskData{}}
		blocks := BuildQueuedCard(pos, queuedTask.TaskID)
		g.postMessage(ts, blocks)
		return
	}

	// No active task for this channel — execute directly.
	req := protocol.TaskRequest{
		Task: event.Text,
	}

	ts := &taskState{
		channelID: event.Channel,
		taskData:  &TaskData{},
	}

	cb := func(evt *protocol.SlackEvent, err error) {
		if err != nil {
			atomic.AddInt64(&metricErrors, 1)
			log.Printf("Stream callback error: %v", err)
			g.postError(ts, err.Error())
			if queuedTask := g.taskQueue.FindByChannel(event.Channel); queuedTask != nil {
				g.taskQueue.MarkError(queuedTask.TaskID)
			}
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
			// Track this task in the queue as running.
			if queuedTask := g.taskQueue.FindByChannel(event.Channel); queuedTask != nil {
				g.taskQueue.MarkRunning(queuedTask.TaskID)
			}
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
			// Debounced: only persist every 10s per task to avoid SQLite spam.
			if evt.Payload != nil && evt.Payload.TaskID != "" && g.stateMgr != nil && g.progressCache.shouldPersist(evt.Payload.TaskID, 10*time.Second) {
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
			g.taskQueue.MarkDone(g.findRunningTaskID(event.Channel))
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
			g.taskQueue.MarkError(g.findRunningTaskID(event.Channel))
		}
	}

	log.Printf("[STREAM] streamWorker=%v, worker=%v", g.streamWorker != nil, g.worker != nil)

	if g.streamWorker != nil {
		// Register task in queue before execution.
		queuedTask := NewQueuedTask(
			g.generateTaskID(),
			event.Channel,
			event.User,
			event.Text,
		)
		log.Printf("[STREAM] Created task %s, marking as running", queuedTask.TaskID)
		g.taskQueue.MarkRunningDirect(queuedTask)
		log.Printf("[STREAM] Calling streamWorker.Execute with task=%q", event.Text)
		if err := g.streamWorker.Execute(req, cb); err != nil {
			atomic.AddInt64(&metricErrors, 1)
			log.Printf("Stream worker error: %v", err)
			g.postError(ts, err.Error())
			g.taskQueue.MarkError(queuedTask.TaskID)
			return
		}
		log.Printf("[STREAM] streamWorker.Execute completed successfully")
	} else {
		log.Printf("[STREAM] Fallback to non-streaming worker")
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

// findRunningTaskID finds the task ID of the running task for the given channel.
func (g *SlackGateway) findRunningTaskID(channelID string) string {
	if g.taskQueue == nil {
		return ""
	}
	for _, t := range g.taskQueue.ListRunning() {
		if t.ChannelID == channelID {
			return t.TaskID
		}
	}
	return ""
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

var taskCounter int64

func (g *SlackGateway) generateTaskID() string {
	counter := atomic.AddInt64(&taskCounter, 1)
	return fmt.Sprintf("task-%d", counter)
}

// Client returns the internal Slack client, allowing callers to reuse it
// instead of creating a redundant slack.New() instance.
func (g *SlackGateway) Client() *slack.Client {
	return g.client
}
