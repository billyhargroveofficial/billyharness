package telegrambot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type telegramPromptAdmission struct {
	UpdateID   int
	InputID    string
	ClientID   string
	InputSeq   int64
	Response   gatewayapi.SessionInputResponse
	State      ChatState
	StateReady bool
	SkipRun    bool
}

func (b *Bot) handleMessage(parent context.Context, msg Message) {
	b.handleMessageWithAdmission(parent, msg, telegramPromptAdmission{})
}

func (b *Bot) handleMessageWithAdmission(parent context.Context, msg Message, admission telegramPromptAdmission) {
	scope := messageChatScope(msg)
	key := scope.Key()
	if !b.allowed(msg) {
		_ = b.sendPlain(parent, msg, "Chat is not allowlisted for this bot.")
		return
	}
	text := strings.TrimSpace(msg.Text)
	if bypassActiveRunLock(text) {
		b.handleCommand(parent, msg, text)
		return
	}
	isCommand := strings.HasPrefix(text, "/")
	inputSeq := admission.InputSeq
	if inputSeq <= 0 {
		inputSeq = b.interruptActiveRunForInput(msg, scope, isCommand)
	}

	mu := b.mutexFor(key)
	mu.Lock()
	defer mu.Unlock()

	if !isCommand && b.inputSuperseded(key, inputSeq) {
		log.Printf("telegram dropped superseded queued message chat=%d key=%s seq=%d", msg.Chat.ID, key, inputSeq)
		b.clearPendingInput(key, admission.InputID)
		return
	}
	if isCommand {
		b.handleCommand(parent, msg, text)
		return
	}
	runCtx, cancel := context.WithCancel(parent)
	b.setCancel(key, cancel)
	defer b.clearCancel(key)
	defer cancel()
	if b.inputSuperseded(key, inputSeq) {
		log.Printf("telegram dropped superseded message before run chat=%d key=%s seq=%d", msg.Chat.ID, key, inputSeq)
		b.clearPendingInput(key, admission.InputID)
		return
	}

	state := admission.State
	if !admission.StateReady {
		var err error
		state, err = b.resolveRunState(runCtx, msg, scope)
		if err != nil {
			_ = b.sendPlain(parent, msg, "Gateway session failed: "+err.Error())
			return
		}
	}

	runReq := gatewayapi.RunRequest{
		InputID:         admission.InputID,
		ClientID:        admission.ClientID,
		Prompt:          text,
		Model:           state.Model,
		Profile:         state.Profile,
		ReasoningEffort: state.ReasoningEffort,
		MaxToolRounds:   b.opts.MaxToolRounds,
		InterruptPolicy: gatewayapi.InterruptPolicyInterrupt,
	}
	runStarted := time.Now()
	firstDelta := time.Time{}
	contentChars := 0
	eventCount := 0
	lastEventSeq := state.LastEventSeq
	var catchupErr error
	state, lastEventSeq, catchupErr = b.replayRunCatchup(runCtx, msg, key, state, lastEventSeq)
	if catchupErr != nil {
		_ = b.sendPlain(parent, msg, "Gateway session failed: "+catchupErr.Error())
		return
	}
	if b.inputSuperseded(key, inputSeq) {
		log.Printf("telegram dropped superseded message after replay catch-up chat=%d key=%s seq=%d", msg.Chat.ID, key, inputSeq)
		b.clearPendingInput(key, admission.InputID)
		return
	}

	live, err := b.startLiveRunView(parent, msg, state)
	if err != nil {
		log.Printf("telegram initial send: %v", err)
		return
	}

	emitEvent := func(event protocol.Event) {
		if b.inputSuperseded(key, inputSeq) {
			return
		}
		if event.Seq > 0 && event.Seq <= lastEventSeq {
			return
		}
		if event.Seq > 0 {
			lastEventSeq = event.Seq
		}
		eventCount++
		if event.Type == protocol.EventAssistantDelta {
			if firstDelta.IsZero() {
				firstDelta = time.Now()
			}
			contentChars += len(fmt.Sprint(event.Data))
		}
		live.Apply(event)
	}
	state, err = b.runGatewaySessionWithRetry(runCtx, msg, key, state, runReq, emitEvent, func(retryState ChatState) {
		state = retryState
		lastEventSeq = 0
		live.Reset(state)
		runStarted = time.Now()
		firstDelta = time.Time{}
		contentChars = 0
		eventCount = 0
	})
	if b.inputSuperseded(key, inputSeq) {
		live.Stop()
		if state.LastEventSeq != lastEventSeq {
			state.LastEventSeq = lastEventSeq
			state.UpdatedAt = time.Now().UTC()
			b.setChatState(key, state)
		}
		b.clearPendingInput(key, admission.InputID)
		if err := b.edit(parent, msg.Chat.ID, live.sent.MessageID, "Interrupted by newer message.", ""); err != nil {
			log.Printf("telegram interrupted edit: %v", err)
		}
		log.Printf("telegram run superseded chat=%d key=%s seq=%d err=%v", msg.Chat.ID, key, inputSeq, err)
		return
	}
	live.Finish(err)
	log.Printf("telegram run finished chat=%d model=%s reasoning=%s duration=%s first_delta=%s chars=%d events=%d err=%v",
		msg.Chat.ID,
		state.Model,
		state.ReasoningEffort,
		time.Since(runStarted).Round(time.Millisecond),
		durationSince(runStarted, firstDelta),
		contentChars,
		eventCount,
		err,
	)
	if live.renderer.ModelCalls > 0 || live.renderer.ToolCalls > 0 {
		state.AgentTurns += live.renderer.ModelCalls
		state.ToolCalls += live.renderer.ToolCalls
	}
	if state.LastEventSeq != lastEventSeq || live.renderer.ModelCalls > 0 || live.renderer.ToolCalls > 0 {
		state.LastEventSeq = lastEventSeq
		state.UpdatedAt = time.Now().UTC()
		b.setChatState(key, state)
	}
	b.clearPendingInput(key, admission.InputID)

	b.deliverRunFinal(parent, msg, live, state)
}

func (b *Bot) admitTelegramPromptUpdate(ctx context.Context, update Update) (telegramPromptAdmission, error) {
	if update.Message == nil {
		return telegramPromptAdmission{}, fmt.Errorf("message required")
	}
	msg := *update.Message
	scope := messageChatScope(msg)
	key := scope.Key()
	state, err := b.resolveRunState(ctx, msg, scope)
	if err != nil {
		return telegramPromptAdmission{}, err
	}
	inputID := telegramInputID(update.UpdateID)
	clientID := telegramClientID(scope)
	resp, err := b.harness.AdmitSessionInput(ctx, state.SessionID, gatewayapi.SessionInputRequest{
		InputID:         inputID,
		Prompt:          strings.TrimSpace(msg.Text),
		InterruptPolicy: gatewayapi.InterruptPolicyInterrupt,
		ClientID:        clientID,
		ClientType:      "telegram",
		Metadata:        telegramInputMetadata(update.UpdateID, msg, scope),
	})
	if err != nil {
		return telegramPromptAdmission{}, err
	}
	if strings.TrimSpace(resp.InputID) == "" {
		resp.InputID = inputID
	}
	if err := b.admit.RecordAdmitted(update.UpdateID, msg, state.SessionID, resp); err != nil {
		return telegramPromptAdmission{}, err
	}
	skipRun := resp.Duplicate && resp.State != "" && resp.State != "admitted"
	if skipRun {
		b.clearPendingInput(key, resp.InputID)
	} else {
		state.PendingInputID = resp.InputID
		state.PendingUpdateID = update.UpdateID
		state.UpdatedAt = time.Now().UTC()
		b.setChatState(key, state)
	}
	log.Printf("telegram admitted update=%d chat=%d key=%s session=%s input=%s state=%s duplicate=%t skip_run=%t",
		update.UpdateID, msg.Chat.ID, key, short(state.SessionID), resp.InputID, resp.State, resp.Duplicate, skipRun)
	return telegramPromptAdmission{
		UpdateID:   update.UpdateID,
		InputID:    resp.InputID,
		ClientID:   clientID,
		Response:   resp,
		State:      state,
		StateReady: true,
		SkipRun:    skipRun,
	}, nil
}

func (b *Bot) runGatewaySessionWithRetry(ctx context.Context, msg Message, key string, state ChatState, runReq gatewayapi.RunRequest, emit func(protocol.Event), beforeRetry func(ChatState)) (ChatState, error) {
	err := b.harness.RunSession(ctx, state.SessionID, runReq, emit)
	if !gatewaySessionMissing(err) {
		return state, err
	}
	log.Printf("telegram gateway session missing; recreating chat=%d old_session=%s", msg.Chat.ID, short(state.SessionID))
	id, createErr := b.createOwnedSession(ctx, msg, state)
	if createErr != nil {
		return state, createErr
	}
	state.SessionID = id
	state.LastEventSeq = 0
	state.UpdatedAt = time.Now().UTC()
	b.setChatState(key, state)
	if beforeRetry != nil {
		beforeRetry(state)
	}
	return state, b.harness.RunSession(ctx, state.SessionID, runReq, emit)
}

func (b *Bot) deliverRunFinal(ctx context.Context, msg Message, live *telegramLiveRunView, state ChatState) {
	if live == nil {
		return
	}
	if b.finishRich(ctx, msg, live.sent, live.renderer, state.Model, state.ReasoningEffort) {
		return
	}
	chunks := live.renderer.FinalChunks(state.Model, state.ReasoningEffort)
	if len(chunks) == 0 {
		if err := b.edit(ctx, msg.Chat.ID, live.sent.MessageID, live.renderer.StatusText(state.Model, state.ReasoningEffort), "HTML"); err != nil {
			log.Printf("telegram final edit: %v", err)
		}
		return
	}
	if err := b.edit(ctx, msg.Chat.ID, live.sent.MessageID, chunks[0], "HTML"); err != nil {
		log.Printf("telegram final edit: %v", err)
	}
	for _, chunk := range chunks[1:] {
		if _, err := b.send(ctx, msg.Chat.ID, msg.ThreadID, chunk, "HTML", false); err != nil {
			log.Printf("telegram final chunk send: %v", err)
			return
		}
	}
}

type telegramLiveRunView struct {
	sent          SentMessage
	renderer      *Renderer
	tools         *ToolProgress
	model         string
	reasoning     string
	contextWindow int64
	mu            sync.Mutex
	answerDirty   bool
	stop          chan struct{}
	typingDone    <-chan struct{}
	editsDone     <-chan struct{}
}

func (b *Bot) startLiveRunView(ctx context.Context, msg Message, state ChatState) (*telegramLiveRunView, error) {
	sent, err := b.send(ctx, msg.Chat.ID, msg.ThreadID, "Billyharness is starting...", "", false)
	if err != nil {
		return nil, err
	}
	view := &telegramLiveRunView{
		sent:          sent,
		renderer:      NewRendererWithContextWindowAndTotals(b.opts.ContextWindow, state.AgentTurns, state.ToolCalls),
		tools:         NewToolProgress(),
		model:         state.Model,
		reasoning:     state.ReasoningEffort,
		contextWindow: b.opts.ContextWindow,
		answerDirty:   true,
		stop:          make(chan struct{}),
	}
	view.typingDone = b.startTypingIndicator(ctx, msg.Chat.ID, view.stop)
	view.editsDone = b.startProgressEdits(ctx, msg.Chat.ID, sent.MessageID, b.opts.EditInterval, view.stop, view.progressText)
	return view, nil
}

func (v *telegramLiveRunView) progressText(force bool, pulse int) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.answerDirty && !force {
		return ""
	}
	v.answerDirty = false
	return v.renderer.StreamPlainTextPulse(v.model, v.reasoning, v.tools, pulse)
}

func (v *telegramLiveRunView) Apply(event protocol.Event) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, rendered := range v.renderer.Apply(event) {
		switch rendered.Kind {
		case "tool", "status":
			if v.tools.Add(rendered) {
				v.answerDirty = true
			}
		case "error":
			v.answerDirty = true
		}
	}
	v.answerDirty = true
}

func (v *telegramLiveRunView) Reset(state ChatState) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.renderer = NewRendererWithContextWindowAndTotals(v.contextWindow, state.AgentTurns, state.ToolCalls)
	v.tools = NewToolProgress()
	v.model = state.Model
	v.reasoning = state.ReasoningEffort
	v.answerDirty = true
}

func (v *telegramLiveRunView) Finish(err error) {
	if v == nil {
		return
	}
	v.mu.Lock()
	if err != nil {
		v.renderer.LastError = err.Error()
	}
	v.tools.Done = true
	v.answerDirty = true
	v.mu.Unlock()
	v.Stop()
}

func (v *telegramLiveRunView) Stop() {
	if v == nil {
		return
	}
	close(v.stop)
	<-v.editsDone
	<-v.typingDone
}

func (b *Bot) interruptActiveRunForInput(msg Message, scope ChatScope, isCommand bool) int64 {
	if isCommand {
		return 0
	}
	key := scope.Key()
	inputSeq := b.markLatestInput(key)
	if b.cancelChat(key) {
		state := b.chatStateWithLegacy(key, scope.LegacyKey())
		if state.SessionID != "" {
			b.cancelGatewaySession(state.SessionID)
		}
		log.Printf("telegram new message interrupted active run chat=%d key=%s seq=%d", msg.Chat.ID, key, inputSeq)
	}
	return inputSeq
}

func (b *Bot) resolveRunState(ctx context.Context, msg Message, scope ChatScope) (ChatState, error) {
	key := scope.Key()
	state := b.chatStateWithLegacy(key, scope.LegacyKey())
	if state.Profile == "" {
		state.Profile = b.opts.Profile
	}
	if state.Model == "" {
		state.Model = b.opts.Model
	}
	if state.ReasoningEffort == "" {
		state.ReasoningEffort = b.opts.ReasoningEffort
	}
	if state.SessionID == "" {
		id, err := b.createOwnedSession(ctx, msg, state)
		if err != nil {
			return state, err
		}
		state.SessionID = id
		state.LastEventSeq = 0
	}
	state.UpdatedAt = time.Now().UTC()
	b.setChatState(key, state)
	return state, nil
}

func (b *Bot) replayRunCatchup(ctx context.Context, msg Message, key string, state ChatState, lastEventSeq int64) (ChatState, int64, error) {
	if state.LastEventSeq <= 0 {
		return state, lastEventSeq, nil
	}
	catchup := NewRendererWithContextWindow(b.opts.ContextWindow)
	catchupEvents := 0
	catchupEmit := func(event protocol.Event) {
		if event.Seq > 0 && event.Seq <= lastEventSeq {
			return
		}
		if event.Seq > 0 {
			lastEventSeq = event.Seq
		}
		catchupEvents++
		catchup.Apply(event)
	}
	if err := b.harness.ReplaySessionEvents(ctx, state.SessionID, state.LastEventSeq, catchupEmit); err != nil {
		if gatewaySessionMissing(err) {
			log.Printf("telegram gateway replay session missing; recreating chat=%d old_session=%s", msg.Chat.ID, short(state.SessionID))
			id, createErr := b.createOwnedSession(ctx, msg, state)
			if createErr != nil {
				return state, lastEventSeq, createErr
			}
			state.SessionID = id
			state.LastEventSeq = 0
			lastEventSeq = 0
			state.UpdatedAt = time.Now().UTC()
			b.setChatState(key, state)
		} else {
			log.Printf("telegram replay events failed chat=%d session=%s after_seq=%d: %v", msg.Chat.ID, short(state.SessionID), state.LastEventSeq, err)
		}
	}
	if catchup.ModelCalls > 0 || catchup.ToolCalls > 0 {
		state.AgentTurns += catchup.ModelCalls
		state.ToolCalls += catchup.ToolCalls
	}
	if state.LastEventSeq != lastEventSeq || catchup.ModelCalls > 0 || catchup.ToolCalls > 0 {
		state.LastEventSeq = lastEventSeq
		state.UpdatedAt = time.Now().UTC()
		b.setChatState(key, state)
	}
	if catchupEvents > 0 {
		log.Printf("telegram replay catch-up chat=%d session=%s events=%d model_calls=%d tool_calls=%d last_seq=%d",
			msg.Chat.ID, short(state.SessionID), catchupEvents, catchup.ModelCalls, catchup.ToolCalls, lastEventSeq)
	}
	return state, lastEventSeq, nil
}
