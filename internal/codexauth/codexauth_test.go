package codexauth

import (
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestJWTClaimsExtractAccountFedRAMPAndExpiration(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	token := testkit.JWT(t, map[string]any{
		"exp": exp,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id":         "acct_nested",
			"chatgpt_account_is_fedramp": true,
		},
	})
	claims := Claims(token)

	if AccountIDFromClaims(claims) != "acct_nested" || AccountIDFromJWT(token) != "acct_nested" {
		t.Fatalf("account claims = %#v", claims)
	}
	if !FedRAMPFromClaims(claims) {
		t.Fatalf("FedRAMPFromClaims = false")
	}
	if ExpirationFromClaims(claims).Unix() != exp || ExpirationFromJWT(token).Unix() != exp {
		t.Fatalf("expiration = %v claims=%#v", ExpirationFromClaims(claims), claims)
	}
}

func TestAuthModeAndTokenPresence(t *testing.T) {
	if got := AuthMode(map[string]any{"personal_access_token": "at-test"}); got != "personalAccessToken" {
		t.Fatalf("PAT auth mode = %q", got)
	}
	if got := AuthMode(map[string]any{"OPENAI_API_KEY": "sk-test"}); got != "apikey" {
		t.Fatalf("API key auth mode = %q", got)
	}
	if !HasTokens(map[string]any{"tokens": map[string]any{"refresh_token": "refresh"}}) {
		t.Fatalf("HasTokens = false")
	}
}

func TestRefreshStatus(t *testing.T) {
	if got := RefreshStatus("at-test", "", time.Time{}, false); got != "not_required" {
		t.Fatalf("PAT refresh status = %q", got)
	}
	if got := RefreshStatus("", "refresh", time.Time{}, false); got != "refresh_required" {
		t.Fatalf("refresh-only status = %q", got)
	}
	if got := RefreshStatus("access", "refresh", time.Now().Add(-time.Hour), false); got != "refresh_required" {
		t.Fatalf("expired status = %q", got)
	}
	if got := RefreshStatus("access", "", time.Now().Add(time.Hour), false); got != "fresh" {
		t.Fatalf("fresh status = %q", got)
	}
}
