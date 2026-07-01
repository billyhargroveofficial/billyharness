package telegrambot

import (
	"context"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

func (b *Bot) nextOffset() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state.Offset
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

func (b *Bot) chatStateWithLegacy(key, legacyKey string) ChatState {
	state := b.chatState(key)
	if !emptyChatState(state) || legacyKey == "" || legacyKey == key {
		return state
	}
	return b.chatState(legacyKey)
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

func (b *Bot) clearPendingInput(key, inputID string) {
	inputID = strings.TrimSpace(inputID)
	if inputID == "" {
		return
	}
	b.saveMu.Lock()
	defer b.saveMu.Unlock()

	b.mu.Lock()
	state := b.state.Chats[key]
	if state.PendingInputID != inputID {
		b.mu.Unlock()
		return
	}
	state.PendingInputID = ""
	state.PendingUpdateID = 0
	state.UpdatedAt = time.Now().UTC()
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

func (b *Bot) markLatestInput(key string) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inputSeq == nil {
		b.inputSeq = map[string]int64{}
	}
	b.inputSeq[key]++
	return b.inputSeq[key]
}

func (b *Bot) inputSuperseded(key string, seq int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return seq > 0 && b.inputSeq[key] != seq
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
	return telegramCommandBypassesRunLock(fields[0])
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

type ChatScope struct {
	ChatID   int64
	ThreadID int
	UserID   int64
}

func messageChatScope(msg Message) ChatScope {
	return ChatScope{
		ChatID:   msg.Chat.ID,
		ThreadID: msg.ThreadID,
		UserID:   messageUserID(msg),
	}
}

func (s ChatScope) LegacyKey() string {
	if s.ThreadID > 0 {
		return strconv.FormatInt(s.ChatID, 10) + ":" + strconv.Itoa(s.ThreadID)
	}
	return strconv.FormatInt(s.ChatID, 10)
}

func (s ChatScope) Key() string {
	key := s.LegacyKey()
	if s.UserID > 0 {
		key += ":u" + strconv.FormatInt(s.UserID, 10)
	}
	return key
}

func chatKey(chatID int64, threadID int) string {
	return (ChatScope{ChatID: chatID, ThreadID: threadID}).LegacyKey()
}

func legacyChatKey(msg Message) string {
	return messageChatScope(msg).LegacyKey()
}

func messageChatKey(msg Message) string {
	return messageChatScope(msg).Key()
}

func messageUserID(msg Message) int64 {
	if msg.From == nil || msg.From.IsBot {
		return 0
	}
	return msg.From.ID
}

func telegramInputID(updateID int) string {
	return "telegram-update-" + strconv.Itoa(updateID)
}

func telegramClientID(scope ChatScope) string {
	return "telegram:" + scope.Key()
}

func telegramInputMetadata(updateID int, msg Message, scope ChatScope) map[string]string {
	metadata := map[string]string{
		"update_id":  strconv.Itoa(updateID),
		"message_id": strconv.Itoa(msg.MessageID),
		"chat_id":    strconv.FormatInt(scope.ChatID, 10),
	}
	if scope.ThreadID > 0 {
		metadata["thread_id"] = strconv.Itoa(scope.ThreadID)
	}
	if scope.UserID > 0 {
		metadata["user_id"] = strconv.FormatInt(scope.UserID, 10)
	}
	return metadata
}

func userChatKey(chatID int64, threadID int, userID int64) string {
	return (ChatScope{ChatID: chatID, ThreadID: threadID, UserID: userID}).Key()
}

func emptyChatState(state ChatState) bool {
	return state == (ChatState{})
}
