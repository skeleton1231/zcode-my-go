package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sha256Sum is S256 challenge of a verifier, base64url.
func sha256Sum(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// TestPKCEVector verifies S256 against RFC 7636's appendix B vector.
func TestPKCEVector(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256Sum(verifier)
	if sum != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("challenge = %q", sum)
	}
	p, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Verifier) < 43 || len(p.Verifier) > 128 {
		t.Fatalf("verifier length = %d", len(p.Verifier))
	}
	if p.Challenge != sha256Sum(p.Verifier) {
		t.Fatal("challenge is not S256(verifier)")
	}
}

// TestAuthorizeLink verifies pi's parameter set on the link.
func TestAuthorizeLink(t *testing.T) {
	p := PKCE{Verifier: "v", Challenge: "c"}
	link := AuthorizeLink(p, 53692)
	for _, want := range []string{
		"https://claude.ai/oauth/authorize?",
		"client_id=" + ClientID,
		"response_type=code",
		"code_challenge=c",
		"code_challenge_method=S256",
		"state=v",
		"redirect_uri=http%3A%2F%2Flocalhost%3A53692%2Fcallback",
		"code=true",
	} {
		if !strings.Contains(link, want) {
			t.Errorf("link missing %q", want)
		}
	}
}

// fakeTokenServer answers the exchange/refresh grants and records bodies.
func fakeTokenServer(t *testing.T) (*httptest.Server, *[]map[string]string) {
	t.Helper()
	var bodies []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if body["grant_type"] == "authorization_code" {
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at-1", "refresh_token": "rt-1", "expires_in": 3600})
			return
		}
		if body["grant_type"] == "refresh_token" {
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at-2", "expires_in": 3600})
			return
		}
		http.Error(w, "bad grant", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func TestExchangeAndRefresh(t *testing.T) {
	srv, bodies := fakeTokenServer(t)

	tok, err := Exchange(context.Background(), srv.URL, "code-1", "state-1", "ver-1", "http://localhost:1/callback")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at-1" || tok.RefreshToken != "rt-1" {
		t.Fatalf("token = %+v", tok)
	}
	// pi 的五分钟偏移。
	if d := time.Until(tok.ExpiresAt); d > 55*time.Minute || d < 54*time.Minute {
		t.Fatalf("expiry skew = %v", d)
	}
	if got := (*bodies)[0]["code_verifier"]; got != "ver-1" {
		t.Fatalf("verifier = %q", got)
	}

	// 刷新：响应无新 refresh token 时保留旧的。
	tok2, err := Refresh(context.Background(), srv.URL, "rt-1")
	if err != nil {
		t.Fatal(err)
	}
	if tok2.AccessToken != "at-2" || tok2.RefreshToken != "rt-1" {
		t.Fatalf("refreshed = %+v", tok2)
	}
	if tok2.Expired() {
		t.Fatal("fresh token reports expired")
	}
}

// TestLoginFullFlow drives the real callback server on an ephemeral port:
// the "browser" is the test itself calling back with the right state.
func TestLoginFullFlow(t *testing.T) {
	srv, _ := fakeTokenServer(t)
	var link string
	got := make(chan Token, 1)
	go func() {
		tok, err := LoginOnPort(context.Background(), 0, func(u string) {
			link = u
		}, srv.URL)
		if err == nil {
			got <- tok
		}
	}()
	// 等 openURL 拿到链接（回调服务器已就绪）。
	deadline := time.Now().Add(3 * time.Second)
	for link == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if link == "" {
		t.Fatal("no authorize link produced")
	}
	// 从链接解析 state 与 redirect_uri，模拟浏览器回调。
	q := strings.SplitN(link, "?", 2)[1]
	params := parseQuery(q)
	resp, err := http.Get(params["redirect_uri"] + "?code=abc&state=" + params["state"])
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case tok := <-got:
		if tok.AccessToken != "at-1" {
			t.Fatalf("token = %+v", tok)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("login did not complete")
	}
}

// TestCallbackRejectsWrongState verifies the server's state check.
func TestCallbackRejectsWrongState(t *testing.T) {
	srv, _ := fakeTokenServer(t)
	linkCh := make(chan string, 1)
	go LoginOnPort(context.Background(), 0, func(u string) { linkCh <- u }, srv.URL)
	var link string
	select {
	case link = <-linkCh:
	case <-time.After(3 * time.Second):
		t.Fatal("no link")
	}
	q := parseQuery(strings.SplitN(link, "?", 2)[1])
	wrong, err := http.Get(params2URL(q["redirect_uri"], "code=x&state=tampered"))
	if err != nil {
		t.Fatal(err)
	}
	if wrong.StatusCode != http.StatusBadRequest {
		t.Fatalf("tampered state accepted: %d", wrong.StatusCode)
	}
	wrong.Body.Close()
}

func parseQuery(q string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(q, "&") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			out[parts[0]] = unescape(parts[1])
		}
	}
	return out
}

func params2URL(base, rawquery string) string { return base + "?" + rawquery }

func unescape(s string) string {
	s = strings.ReplaceAll(s, "%3A", ":")
	s = strings.ReplaceAll(s, "%2F", "/")
	return s
}
