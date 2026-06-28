package telegrambot

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type Options struct {
	BotToken         string
	BotAPIBaseURL    string
	GatewayURL       string
	StatePath        string
	Model            string
	Profile          string
	ReasoningEffort  string
	MaxToolRounds    int
	PollTimeoutSec   int
	EditInterval     time.Duration
	AllowedChatIDs   map[int64]bool
	AllowedUserIDs   map[int64]bool
	AllowAllChats    bool
	SendEnabled      bool
	DryRunDefault    bool
	RequireAllowlist bool
}

type Bot struct {
	opts    Options
	client  *Client
	harness Harness
	store   Store
	state   State

	mu      sync.Mutex
	saveMu  sync.Mutex
	chatMux map[string]*sync.Mutex
	cancel  map[string]context.CancelFunc
}

const telegramEditTimeout = 15 * time.Second

func New(opts Options, client *Client, harness Harness) (*Bot, error) {
	if strings.TrimSpace(opts.BotToken) == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	if opts.StatePath == "" {
		return nil, fmt.Errorf("telegram state path required")
	}
	if opts.PollTimeoutSec <= 0 {
		opts.PollTimeoutSec = 30
	}
	if opts.EditInterval <= 0 {
		opts.EditInterval = 700 * time.Millisecond
	}
	if opts.AllowAllChats {
		opts.RequireAllowlist = false
	} else if opts.SendEnabled && !opts.DryRunDefault {
		opts.RequireAllowlist = true
	}
	opts.Profile = config.NormalizeProfileName(opts.Profile)
	if client == nil {
		client = NewClient(ClientOptions{
			BaseURL:     opts.BotAPIBaseURL,
			Token:       opts.BotToken,
			MinInterval: opts.EditInterval,
		})
	}
	if harness == nil {
		harness = NewGatewayClient(opts.GatewayURL)
	}
	store := Store{Path: opts.StatePath}
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	return &Bot{
		opts:    opts,
		client:  client,
		harness: harness,
		store:   store,
		state:   state,
		chatMux: map[string]*sync.Mutex{},
		cancel:  map[string]context.CancelFunc{},
	}, nil
}

func (b *Bot) Run(ctx context.Context) error {
	if b.opts.SendEnabled && !b.opts.DryRunDefault {
		if err := b.client.DeleteWebhook(ctx); err != nil {
			return err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		updates, err := b.client.GetUpdates(ctx, b.nextOffset(), b.opts.PollTimeoutSec)
		if err != nil {
			log.Printf("telegram getUpdates: %v", err)
			sleep(ctx, 2*time.Second)
			continue
		}
		for _, update := range updates {
			b.ackOffset(update.UpdateID)
			if update.Message == nil || strings.TrimSpace(update.Message.Text) == "" {
				continue
			}
			msg := *update.Message
			go b.handleMessage(ctx, msg)
		}
	}
}

func (b *Bot) handleMessage(parent context.Context, msg Message) {
	key := chatKey(msg.Chat.ID, msg.ThreadID)
	if !b.allowed(msg) {
		_ = b.sendPlain(parent, msg, "Chat is not allowlisted for this bot.")
		return
	}
	text := strings.TrimSpace(msg.Text)
	if bypassActiveRunLock(text) {
		b.handleCommand(parent, msg, text)
		return
	}

	mu := b.mutexFor(key)
	mu.Lock()
	defer mu.Unlock()

	if strings.HasPrefix(text, "/") {
		b.handleCommand(parent, msg, text)
		return
	}
	runCtx, cancel := context.WithCancel(parent)
	b.setCancel(key, cancel)
	defer b.clearCancel(key)
	defer cancel()

	state := b.chatState(key)
	if state.Profile == "" {
		state.Profile = b.opts.Profile
	}
	if state.SessionID == "" {
		id, err := b.harness.CreateSession(runCtx, state.Profile)
		if err != nil {
			_ = b.sendPlain(parent, msg, "Gateway session failed: "+err.Error())
			return
		}
		state.SessionID = id
	}
	if state.Model == "" {
		state.Model = b.opts.Model
	}
	if state.ReasoningEffort == "" {
		state.ReasoningEffort = b.opts.ReasoningEffort
	}
	state.UpdatedAt = time.Now().UTC()
	b.setChatState(key, state)

	sent, err := b.send(parent, msg.Chat.ID, msg.ThreadID, "Billyharness is starting...", "", false)
	if err != nil {
		log.Printf("telegram initial send: %v", err)
		return
	}
	renderer := NewRenderer()
	tools := NewToolProgress()
	var renderMu sync.Mutex
	answerDirty := true
	stopStreamEdits := make(chan struct{})
	streamEditsDone := make(chan struct{})
	go func() {
		defer close(streamEditsDone)
		ticker := time.NewTicker(b.opts.EditInterval)
		defer ticker.Stop()
		lastText := ""
		flush := func() {
			renderMu.Lock()
			body := ""
			if answerDirty {
				body = renderer.StreamPlainText(state.Model, state.ReasoningEffort, tools)
				answerDirty = false
			}
			renderMu.Unlock()

			if body != "" && body != lastText {
				lastText = body
				if err := b.edit(parent, msg.Chat.ID, sent.MessageID, body, ""); err != nil {
					log.Printf("telegram edit: %v", err)
				}
			}
		}
		for {
			select {
			case <-stopStreamEdits:
				return
			case <-ticker.C:
				flush()
			}
		}
	}()

	runReq := gateway.RunRequest{
		Prompt:          text,
		Model:           state.Model,
		Profile:         state.Profile,
		ReasoningEffort: state.ReasoningEffort,
		MaxToolRounds:   b.opts.MaxToolRounds,
	}
	runStarted := time.Now()
	firstDelta := time.Time{}
	contentChars := 0
	eventCount := 0
	emitEvent := func(event protocol.Event) {
		eventCount++
		if event.Type == protocol.EventAssistantDelta {
			if firstDelta.IsZero() {
				firstDelta = time.Now()
			}
			contentChars += len(fmt.Sprint(event.Data))
		}
		renderMu.Lock()
		defer renderMu.Unlock()
		for _, rendered := range renderer.Apply(event) {
			switch rendered.Kind {
			case "tool":
				if tools.Add(rendered) {
					answerDirty = true
				}
			case "error":
				answerDirty = true
			}
		}
		answerDirty = true
	}
	err = b.harness.RunSession(runCtx, state.SessionID, runReq, emitEvent)
	if gatewaySessionMissing(err) {
		log.Printf("telegram gateway session missing; recreating chat=%d old_session=%s", msg.Chat.ID, short(state.SessionID))
		id, createErr := b.harness.CreateSession(runCtx, state.Profile)
		if createErr != nil {
			err = createErr
		} else {
			state.SessionID = id
			state.UpdatedAt = time.Now().UTC()
			b.setChatState(key, state)
			renderMu.Lock()
			renderer = NewRenderer()
			tools = NewToolProgress()
			answerDirty = true
			renderMu.Unlock()
			runStarted = time.Now()
			firstDelta = time.Time{}
			contentChars = 0
			eventCount = 0
			err = b.harness.RunSession(runCtx, state.SessionID, runReq, emitEvent)
		}
	}
	renderMu.Lock()
	if err != nil {
		renderer.LastError = err.Error()
	}
	tools.Done = true
	answerDirty = true
	renderMu.Unlock()
	close(stopStreamEdits)
	<-streamEditsDone
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

	if b.finishRich(parent, msg, sent, renderer, state.Model, state.ReasoningEffort) {
		return
	}

	chunks := renderer.FinalChunks(state.Model, state.ReasoningEffort)
	if len(chunks) == 0 {
		if err := b.edit(parent, msg.Chat.ID, sent.MessageID, renderer.StatusText(state.Model, state.ReasoningEffort), "HTML"); err != nil {
			log.Printf("telegram final edit: %v", err)
		}
		return
	}
	if err := b.edit(parent, msg.Chat.ID, sent.MessageID, chunks[0], "HTML"); err != nil {
		log.Printf("telegram final edit: %v", err)
	}
	for _, chunk := range chunks[1:] {
		if _, err := b.send(parent, msg.Chat.ID, msg.ThreadID, chunk, "HTML", false); err != nil {
			log.Printf("telegram final chunk send: %v", err)
			return
		}
	}
}

func (b *Bot) finishRich(ctx context.Context, msg Message, placeholder SentMessage, renderer *Renderer, model, reasoning string) bool {
	chunks := renderer.FinalRichMarkdownChunks(model, reasoning)
	if len(chunks) == 0 {
		return false
	}
	placeholderRich := false
	var freshRichMessageIDs []int
	if err := b.editRichMarkdown(ctx, msg.Chat.ID, placeholder.MessageID, chunks[0]); err != nil {
		log.Printf("telegram final rich edit failed, trying fresh rich message: %v", err)
		created, sendErr := b.sendRichMarkdown(ctx, msg.Chat.ID, msg.ThreadID, chunks[0])
		if sendErr != nil {
			log.Printf("telegram final rich send failed, falling back to HTML: %v", sendErr)
			return false
		}
		freshRichMessageIDs = append(freshRichMessageIDs, created.MessageID)
	} else {
		placeholderRich = true
	}
	for _, chunk := range chunks[1:] {
		created, err := b.sendRichMarkdown(ctx, msg.Chat.ID, msg.ThreadID, chunk)
		if err != nil {
			log.Printf("telegram final rich chunk send failed: %v", err)
			for _, messageID := range freshRichMessageIDs {
				b.delete(ctx, msg.Chat.ID, messageID)
			}
			return false
		}
		freshRichMessageIDs = append(freshRichMessageIDs, created.MessageID)
	}
	if !placeholderRich {
		b.delete(ctx, msg.Chat.ID, placeholder.MessageID)
	}
	return true
}

func (b *Bot) handleCommand(ctx context.Context, msg Message, text string) {
	key := chatKey(msg.Chat.ID, msg.ThreadID)
	fields := strings.Fields(text)
	cmd := strings.ToLower(strings.SplitN(fields[0], "@", 2)[0])
	arg := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	switch cmd {
	case "/start", "/help":
		_ = b.sendHTML(ctx, msg, HelpHTML())
	case "/new", "/reset":
		state := b.chatState(key)
		if state.Profile == "" {
			state.Profile = b.opts.Profile
		}
		id, err := b.harness.CreateSession(ctx, state.Profile)
		if err != nil {
			_ = b.sendPlain(ctx, msg, "Gateway session failed: "+err.Error())
			return
		}
		state.SessionID = id
		state.UpdatedAt = time.Now().UTC()
		b.setChatState(key, state)
		_ = b.sendPlain(ctx, msg, "New Billyharness session: "+short(id))
	case "/status":
		state := b.chatState(key)
		_ = b.sendHTML(ctx, msg, StatusHTML(state, b.opts))
	case "/model":
		state := b.chatState(key)
		if arg == "" {
			_ = b.sendPlain(ctx, msg, "Current model: "+fallback(state.Model, b.opts.Model))
			return
		}
		state.Model = modelAlias(arg)
		state.UpdatedAt = time.Now().UTC()
		b.setChatState(key, state)
		_ = b.sendPlain(ctx, msg, "Model: "+state.Model)
	case "/profile":
		state := b.chatState(key)
		if arg == "" {
			_ = b.sendPlain(ctx, msg, "Current profile: "+fallback(state.Profile, b.opts.Profile))
			return
		}
		state.Profile = config.NormalizeProfileName(arg)
		state.SessionID = ""
		state.UpdatedAt = time.Now().UTC()
		b.setChatState(key, state)
		_ = b.sendPlain(ctx, msg, "Profile: "+state.Profile+"; next message starts a new session")
	case "/reasoning":
		state := b.chatState(key)
		if arg == "" {
			_ = b.sendPlain(ctx, msg, "Current reasoning: "+fallback(state.ReasoningEffort, b.opts.ReasoningEffort))
			return
		}
		state.ReasoningEffort = strings.ToLower(strings.TrimSpace(arg))
		state.UpdatedAt = time.Now().UTC()
		b.setChatState(key, state)
		_ = b.sendPlain(ctx, msg, "Reasoning: "+state.ReasoningEffort)
	case "/mcp":
		status, err := b.harness.MCPStatus(ctx)
		if err != nil {
			_ = b.sendPlain(ctx, msg, "MCP status failed: "+err.Error())
			return
		}
		_ = b.sendHTML(ctx, msg, "<b>MCP</b>\n<pre>"+esc(status)+"</pre>")
	case "/cancel":
		state := b.chatState(key)
		localCancelled := b.cancelChat(key)
		if state.SessionID != "" {
			b.cancelGatewaySession(state.SessionID)
		}
		if localCancelled {
			_ = b.sendPlain(ctx, msg, "Cancelled current run.")
		} else if state.SessionID != "" {
			_ = b.sendPlain(ctx, msg, "Cancel requested.")
		} else {
			_ = b.sendPlain(ctx, msg, "No active run.")
		}
	default:
		_ = b.sendPlain(ctx, msg, "Unknown command. Use /help.")
	}
}

func (b *Bot) sendHTML(ctx context.Context, msg Message, text string) error {
	_, err := b.send(ctx, msg.Chat.ID, msg.ThreadID, text, "HTML", false)
	return err
}

func (b *Bot) sendPlain(ctx context.Context, msg Message, text string) error {
	_, err := b.send(ctx, msg.Chat.ID, msg.ThreadID, text, "", false)
	return err
}

func (b *Bot) send(ctx context.Context, chatID int64, threadID int, text, parseMode string, force bool) (SentMessage, error) {
	if !b.opts.SendEnabled || (b.opts.DryRunDefault && !force) {
		log.Printf("telegram dry-run send chat=%d thread=%d text=%q", chatID, threadID, preview(text, 300))
		return SentMessage{MessageID: int(time.Now().UnixNano() % 1_000_000), Chat: Chat{ID: chatID}}, nil
	}
	return b.client.SendMessage(ctx, chatID, text, parseMode, threadID)
}

func (b *Bot) edit(ctx context.Context, chatID int64, messageID int, text, parseMode string) error {
	if !b.opts.SendEnabled || b.opts.DryRunDefault {
		log.Printf("telegram dry-run edit chat=%d message=%d text=%q", chatID, messageID, preview(text, 300))
		return nil
	}
	editCtx, cancel := telegramEditContext(ctx)
	defer cancel()
	err := b.client.EditMessageText(editCtx, chatID, messageID, text, parseMode)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		return nil
	}
	return err
}

func (b *Bot) sendRichMarkdown(ctx context.Context, chatID int64, threadID int, markdown string) (SentMessage, error) {
	if !b.opts.SendEnabled || b.opts.DryRunDefault {
		log.Printf("telegram dry-run send-rich chat=%d thread=%d text=%q", chatID, threadID, preview(markdown, 300))
		return SentMessage{MessageID: int(time.Now().UnixNano() % 1_000_000), Chat: Chat{ID: chatID}}, nil
	}
	return b.client.SendRichMessageMarkdown(ctx, chatID, markdown, threadID)
}

func (b *Bot) editRichMarkdown(ctx context.Context, chatID int64, messageID int, markdown string) error {
	if !b.opts.SendEnabled || b.opts.DryRunDefault {
		log.Printf("telegram dry-run edit-rich chat=%d message=%d text=%q", chatID, messageID, preview(markdown, 300))
		return nil
	}
	editCtx, cancel := telegramEditContext(ctx)
	defer cancel()
	err := b.client.EditMessageRichMarkdown(editCtx, chatID, messageID, markdown)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		return nil
	}
	return err
}

func telegramEditContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, telegramEditTimeout)
}

func (b *Bot) delete(ctx context.Context, chatID int64, messageID int) {
	if !b.opts.SendEnabled || b.opts.DryRunDefault {
		log.Printf("telegram dry-run delete chat=%d message=%d", chatID, messageID)
		return
	}
	if err := b.client.DeleteMessage(ctx, chatID, messageID); err != nil {
		log.Printf("telegram delete: %v", err)
	}
}

func (b *Bot) nextOffset() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state.Offset
}

func (b *Bot) allowed(msg Message) bool {
	if b.opts.AllowAllChats {
		return true
	}
	if len(b.opts.AllowedChatIDs) == 0 && len(b.opts.AllowedUserIDs) == 0 {
		return !b.opts.RequireAllowlist
	}
	if b.opts.AllowedChatIDs[msg.Chat.ID] {
		return true
	}
	if msg.From != nil && b.opts.AllowedUserIDs[msg.From.ID] {
		return true
	}
	return false
}

func (b *Bot) mutexFor(key string) *sync.Mutex {
	b.mu.Lock()
	defer b.mu.Unlock()
	mu := b.chatMux[key]
	if mu == nil {
		mu = &sync.Mutex{}
		b.chatMux[key] = mu
	}
	return mu
}

func (b *Bot) chatState(key string) ChatState {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.state.Chats[key]
	return state
}

func (b *Bot) setChatState(key string, state ChatState) {
	b.saveMu.Lock()
	defer b.saveMu.Unlock()

	b.mu.Lock()
	if b.state.Chats == nil {
		b.state.Chats = map[string]ChatState{}
	}
	b.state.Chats[key] = state
	snapshot := cloneState(b.state)
	b.mu.Unlock()
	if err := b.store.Save(snapshot); err != nil {
		log.Printf("telegram state save: %v", err)
	}
}

func (b *Bot) ackOffset(updateID int) {
	b.saveMu.Lock()
	defer b.saveMu.Unlock()

	b.mu.Lock()
	if updateID >= b.state.Offset {
		b.state.Offset = updateID + 1
	}
	snapshot := cloneState(b.state)
	b.mu.Unlock()
	if err := b.store.Save(snapshot); err != nil {
		log.Printf("telegram state save: %v", err)
	}
}

func (b *Bot) setCancel(key string, cancel context.CancelFunc) {
	b.mu.Lock()
	b.cancel[key] = cancel
	b.mu.Unlock()
}

func (b *Bot) clearCancel(key string) {
	b.mu.Lock()
	delete(b.cancel, key)
	b.mu.Unlock()
}

func (b *Bot) cancelChat(key string) bool {
	b.mu.Lock()
	cancel := b.cancel[key]
	b.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (b *Bot) cancelGatewaySession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || b.harness == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cancelled, err := b.harness.CancelSession(ctx, sessionID)
		if err != nil {
			log.Printf("telegram gateway cancel session=%s failed: %v", short(sessionID), err)
			return
		}
		log.Printf("telegram gateway cancel session=%s cancelled=%t", short(sessionID), cancelled)
	}()
}

func bypassActiveRunLock(text string) bool {
	if !strings.HasPrefix(text, "/") {
		return false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	cmd := strings.ToLower(strings.SplitN(fields[0], "@", 2)[0])
	switch cmd {
	case "/cancel", "/status", "/start", "/help":
		return true
	default:
		return false
	}
}

func cloneState(state State) State {
	out := State{
		Offset: state.Offset,
		Chats:  map[string]ChatState{},
	}
	for key, value := range state.Chats {
		out.Chats[key] = value
	}
	return out
}

func chatKey(chatID int64, threadID int) string {
	if threadID > 0 {
		return strconv.FormatInt(chatID, 10) + ":" + strconv.Itoa(threadID)
	}
	return strconv.FormatInt(chatID, 10)
}

func HelpHTML() string {
	return `<b>Billyharness Telegram</b>
Send a message to run the agent.

Commands:
<code>/new</code> new session
<code>/status</code> current chat settings
<code>/model flash|pro|gpt|gpt-5.5</code>
<code>/profile billy</code>
<code>/reasoning low|medium|high|xhigh|off</code>
<code>/mcp</code> MCP status
<code>/cancel</code> cancel current run`
}

func StatusHTML(state ChatState, opts Options) string {
	var allowedChats []string
	for chat := range opts.AllowedChatIDs {
		allowedChats = append(allowedChats, strconv.FormatInt(chat, 10))
	}
	sort.Strings(allowedChats)
	var allowedUsers []string
	for user := range opts.AllowedUserIDs {
		allowedUsers = append(allowedUsers, strconv.FormatInt(user, 10))
	}
	sort.Strings(allowedUsers)
	if opts.AllowAllChats {
		allowedChats = []string{"all chats"}
	} else if len(allowedChats) == 0 {
		allowedChats = []string{"not configured"}
	}
	if len(allowedUsers) == 0 {
		allowedUsers = []string{"not configured"}
	}
	return "<b>Status</b>\n" +
		"session: <code>" + esc(short(state.SessionID)) + "</code>\n" +
		"model: <code>" + esc(fallback(state.Model, opts.Model)) + "</code>\n" +
		"profile: <code>" + esc(fallback(state.Profile, opts.Profile)) + "</code>\n" +
		"reasoning: <code>" + esc(fallback(state.ReasoningEffort, opts.ReasoningEffort)) + "</code>\n" +
		"send: <code>" + esc(fmt.Sprint(opts.SendEnabled && !opts.DryRunDefault)) + "</code>\n" +
		"allowed chats: <code>" + esc(strings.Join(allowedChats, ",")) + "</code>\n" +
		"allowed users: <code>" + esc(strings.Join(allowedUsers, ",")) + "</code>"
}

func (r *Renderer) StreamPlainText(model, reasoning string, tools *ToolProgress) string {
	content := strings.TrimSpace(r.Content.String())
	if content == "" {
		content = "Working..."
	}
	elapsed := time.Since(r.Started).Round(time.Second)
	header := "⚡ Billyharness · Running\n" +
		"🧬 " + model + " · 🧠 " + reasoning + " · ⏱ " + elapsed.String() + "\n\n"
	var suffixParts []string
	if toolText := tools.PlainText(); toolText != "" {
		suffixParts = append(suffixParts, toolText)
	}
	suffixParts = append(suffixParts, r.footerLine())
	suffix := "\n\n" + strings.Join(suffixParts, "\n\n")
	budget := telegramLimit - telegramUTF16Len(header) - telegramUTF16Len(suffix) - 16
	if budget < 800 {
		budget = 800
	}
	text := header + streamContentPreview(content, budget) + suffix
	return trimTelegram(text)
}

func streamContentPreview(content string, budget int) string {
	if budget <= 0 || telegramUTF16Len(content) <= budget {
		return content
	}
	prefix := "…\n"
	available := budget - telegramUTF16Len(prefix)
	if available <= 0 {
		return prefix
	}
	runes := []rune(content)
	n := telegramRuneSuffixLen(runes, available)
	return prefix + string(runes[len(runes)-n:])
}

func telegramRuneSuffixLen(runes []rune, limit int) int {
	if len(runes) == 0 {
		return 0
	}
	used := 0
	for i := len(runes) - 1; i >= 0; i-- {
		next := 1
		if runes[i] > 0xFFFF {
			next = 2
		}
		if used+next > limit {
			return max(1, len(runes)-1-i)
		}
		used += next
	}
	return len(runes)
}

func modelAlias(value string) string {
	return modelinfo.NormalizeAlias(value)
}

func fallback(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func short(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func preview(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func gatewaySessionMissing(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "gateway run http 404") && strings.Contains(text, "session not found")
}

func durationSince(start, mark time.Time) string {
	if mark.IsZero() {
		return "n/a"
	}
	return mark.Sub(start).Round(time.Millisecond).String()
}

func DefaultStatePath() string {
	home := os.Getenv("BILLYHARNESS_HOME")
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil && userHome != "" {
			home = userHome + "/billyharness"
		} else {
			home = ".billyharness"
		}
	}
	return home + "/telegram/state.json"
}
