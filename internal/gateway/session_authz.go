package gateway

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

type sessionAccessKind int

const (
	sessionAccessRead sessionAccessKind = iota
	sessionAccessMutate
)

func (s *Server) sessionForRequest(w http.ResponseWriter, r *http.Request, access sessionAccessKind) (*Session, bool) {
	session, ok := s.session(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return nil, false
	}
	if err := authorizeSessionAccess(session, sessionOwnerFromRequest(r), access); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return nil, false
	}
	return session, true
}

func sessionOwnerFromRequest(r *http.Request) gatewayapi.SessionOwner {
	if r == nil {
		return gatewayapi.SessionOwner{}
	}
	return normalizeSessionOwner(gatewayapi.SessionOwner{
		ClientType:       r.Header.Get(gatewayapi.HeaderSessionClientType),
		TelegramChatID:   parseInt64Header(r, gatewayapi.HeaderSessionTelegramChatID),
		TelegramThreadID: parseIntHeader(r, gatewayapi.HeaderSessionTelegramThreadID),
		TelegramUserID:   parseInt64Header(r, gatewayapi.HeaderSessionTelegramUserID),
		TUIChatID:        r.Header.Get(gatewayapi.HeaderSessionTUIChatID),
	})
}

func parseInt64Header(r *http.Request, header string) int64 {
	value := strings.TrimSpace(r.Header.Get(header))
	if value == "" {
		return 0
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func parseIntHeader(r *http.Request, header string) int {
	value := strings.TrimSpace(r.Header.Get(header))
	if value == "" {
		return 0
	}
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func authorizeSessionAccess(session *Session, actor gatewayapi.SessionOwner, access sessionAccessKind) error {
	if session == nil || sessionOwnerEmpty(actor) {
		return nil
	}
	owner := normalizeSessionOwner(session.Owner)
	if sessionOwnerEmpty(owner) {
		if access == sessionAccessRead {
			return nil
		}
		return fmt.Errorf("session is legacy unowned and read-only for scoped clients")
	}
	if sessionOwnerPrincipalMatches(owner, actor) {
		return nil
	}
	return fmt.Errorf("session owner scope mismatch")
}

func sessionOwnerEmpty(owner gatewayapi.SessionOwner) bool {
	owner = normalizeSessionOwner(owner)
	return owner.ClientType == "" &&
		owner.TelegramChatID == 0 &&
		owner.TelegramThreadID == 0 &&
		owner.TelegramUserID == 0 &&
		owner.TUIChatID == ""
}

func sessionOwnerPrincipalMatches(owner, actor gatewayapi.SessionOwner) bool {
	owner = normalizeSessionOwner(owner)
	actor = normalizeSessionOwner(actor)
	if owner.ClientType != "" && actor.ClientType != "" && owner.ClientType != actor.ClientType {
		return false
	}
	switch owner.ClientType {
	case "telegram":
		if owner.TelegramChatID != 0 && actor.TelegramChatID != owner.TelegramChatID {
			return false
		}
		if owner.TelegramThreadID != 0 && actor.TelegramThreadID != owner.TelegramThreadID {
			return false
		}
		if owner.TelegramUserID != 0 && actor.TelegramUserID != owner.TelegramUserID {
			return false
		}
		return actor.TelegramChatID != 0 || actor.TelegramUserID != 0
	case "tui":
		if owner.TUIChatID != "" {
			return actor.TUIChatID == owner.TUIChatID
		}
		return actor.ClientType == "tui"
	default:
		if owner.ClientType != "" {
			return actor.ClientType == owner.ClientType
		}
		return false
	}
}

func sessionOwnerBodyMatchesActor(owner, actor gatewayapi.SessionOwner) bool {
	if sessionOwnerEmpty(actor) {
		return true
	}
	if sessionOwnerEmpty(owner) {
		return true
	}
	return sessionOwnerPrincipalMatches(owner, actor)
}
