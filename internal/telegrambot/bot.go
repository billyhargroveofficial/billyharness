package telegrambot

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
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
	ContextWindow    int64
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
	admit   telegramAdmissionStore
	state   State

	mu       sync.Mutex
	saveMu   sync.Mutex
	chatMux  map[string]*sync.Mutex
	cancel   map[string]context.CancelFunc
	inputSeq map[string]int64
}

const telegramEditTimeout = 15 * time.Second
const telegramProgressEditTimeout = 8 * time.Second

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
	opts = normalizeAllowlistOptions(opts)
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
		opts:     opts,
		client:   client,
		harness:  harness,
		store:    store,
		admit:    newTelegramAdmissionStore(opts.StatePath),
		state:    state,
		chatMux:  map[string]*sync.Mutex{},
		cancel:   map[string]context.CancelFunc{},
		inputSeq: map[string]int64{},
	}, nil
}
