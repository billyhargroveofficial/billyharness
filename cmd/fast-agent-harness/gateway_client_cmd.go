package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func chatGateway(baseURL string, noReasoning bool, model, profile string, mock bool) error {
	baseURL = normalizeGatewayURL(baseURL)
	profile = config.NormalizeProfileName(profile)
	sessionID, err := gatewayCreateSession(context.Background(), baseURL, profile)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Fprintln(os.Stderr, "fast-agent-harness gateway chat. Session "+sessionID+". Type /exit or press Ctrl-D to quit.")
	for {
		fmt.Fprint(os.Stderr, "> ")
		if !scanner.Scan() {
			fmt.Fprintln(os.Stderr)
			return scanner.Err()
		}
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" {
			continue
		}
		if prompt == "/exit" || prompt == "/quit" {
			return nil
		}
		path := "/v1/sessions/" + sessionID + "/run"
		req := gateway.RunRequest{Prompt: prompt, Model: model, Profile: profile}
		if mock {
			req.Provider = "mock"
			req.Model = "mock"
		}
		if err := gatewayRun(context.Background(), baseURL, path, req, terminalEmitter(noReasoning)); err != nil {
			return err
		}
	}
}

func gatewayCreateSession(ctx context.Context, baseURL, profile string) (string, error) {
	baseURL = normalizeGatewayURL(baseURL)
	body, err := json.Marshal(gateway.CreateSessionRequest{Profile: profile})
	if err != nil {
		return "", err
	}
	resp, err := gateway.DoWithReadyRetry(ctx, http.DefaultClient, baseURL, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/sessions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		gateway.SetAuthHeaderFromEnv(req)
		return req, nil
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("gateway returned empty session id")
	}
	return out.ID, nil
}

func gatewayRun(ctx context.Context, baseURL, path string, runReq gateway.RunRequest, emit func(protocol.Event)) error {
	baseURL = normalizeGatewayURL(baseURL)
	body, _ := json.Marshal(runReq)
	resp, err := gateway.DoWithReadyRetry(ctx, http.DefaultClient, baseURL, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		gateway.SetAuthHeaderFromEnv(req)
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var event protocol.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return err
		}
		emit(event)
		if event.Type == protocol.EventRunFailed {
			return fmt.Errorf("%v", event.Data)
		}
	}
	return scanner.Err()
}
