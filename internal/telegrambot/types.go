package telegrambot

import "time"

type Update struct {
	UpdateID int      `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

type Message struct {
	MessageID       int         `json:"message_id"`
	From            *User       `json:"from,omitempty"`
	Chat            Chat        `json:"chat"`
	Date            int64       `json:"date,omitempty"`
	Text            string      `json:"text,omitempty"`
	Caption         string      `json:"caption,omitempty"`
	ThreadID        int         `json:"message_thread_id,omitempty"`
	Entities        []Entity    `json:"entities,omitempty"`
	CaptionEntities []Entity    `json:"caption_entities,omitempty"`
	Photo           []PhotoSize `json:"photo,omitempty"`
	Document        *Document   `json:"document,omitempty"`
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type,omitempty"`
	Title    string `json:"title,omitempty"`
	Username string `json:"username,omitempty"`
}

type Entity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

type Document struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	FileName     string `json:"file_name,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

type TelegramFile struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
}

type SentMessage struct {
	MessageID int  `json:"message_id"`
	Chat      Chat `json:"chat"`
}

type InputRichMessage struct {
	HTML                string `json:"html,omitempty"`
	Markdown            string `json:"markdown,omitempty"`
	IsRTL               bool   `json:"is_rtl,omitempty"`
	SkipEntityDetection bool   `json:"skip_entity_detection,omitempty"`
}

type botAPIResponse[T any] struct {
	OK          bool       `json:"ok"`
	Result      T          `json:"result,omitempty"`
	ErrorCode   int        `json:"error_code,omitempty"`
	Description string     `json:"description,omitempty"`
	Parameters  parameters `json:"parameters,omitempty"`
}

type parameters struct {
	RetryAfter int `json:"retry_after,omitempty"`
}

type ChatState struct {
	SessionID          string    `json:"session_id,omitempty"`
	Model              string    `json:"model,omitempty"`
	Profile            string    `json:"profile,omitempty"`
	ReasoningEffort    string    `json:"reasoning_effort,omitempty"`
	AccessMode         string    `json:"access_mode,omitempty"`
	AgentTurns         int       `json:"agent_turns,omitempty"`
	ToolCalls          int       `json:"tool_calls,omitempty"`
	LastEventSeq       int64     `json:"last_event_seq,omitempty"`
	PendingInputID     string    `json:"pending_input_id,omitempty"`
	PendingUpdateID    int       `json:"pending_update_id,omitempty"`
	PendingUserInputID string    `json:"pending_user_input_id,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type State struct {
	Offset int                  `json:"offset"`
	Chats  map[string]ChatState `json:"chats"`
}
