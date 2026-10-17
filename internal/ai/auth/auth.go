// Package auth implements the Anthropic OAuth login (Claude Pro/Max),
// translated from pi's packages/ai/src/auth/oauth/anthropic.ts: PKCE with
// S256, a local callback server, the authorization-code exchange, and
// token refresh.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Anthropic OAuth endpoints and identity (pi's constants).
const (
	ClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	AuthorizeURL = "https://claude.ai/oauth/authorize"
	TokenURL     = "https://platform.claude.com/v1/oauth/token"
	CallbackHost = "127.0.0.1"
	CallbackPort = 53692
	CallbackPath = "/callback"
	OAuthScopes  = "org:create_api_key user:profile user:inference " +
		"user:sessions:claude_code user:mcp_servers user:file_upload"
)

// loginTimeout bounds the whole browser round-trip.
const loginTimeout = 5 * time.Minute

// Token is a stored OAuth credential.
type Token struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// Expired reports whether the token needs a refresh (a five-minute skew,
// as pi's exchange applies).
func (t Token) Expired() bool {
	return time.Now().After(t.ExpiresAt)
}

// PKCE holds a verifier and its S256 challenge.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE generates a random verifier (pi: 43-128 chars, base64url) and
// its S256 challenge.
func NewPKCE() (PKCE, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return PKCE{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

// redirectURI is the local callback's redirect target.
func redirectURI(port int) string {
	return fmt.Sprintf("http://localhost:%d%s", port, CallbackPath)
}

// AuthorizeLink builds the browser URL (pi's parameter set, state=verifier).
func AuthorizeLink(p PKCE, port int) string {
	q := url.Values{
		"code":                  {"true"},
		"client_id":             {ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI(port)},
		"scope":                 {OAuthScopes},
		"code_challenge":        {p.Challenge},
		"code_challenge_method": {"S256"},
		"state":                 {p.Verifier},
	}
	return AuthorizeURL + "?" + q.Encode()
}

// Login runs the whole flow: a local callback server (pi's
// startOAuthCallbackServer), the browser link, the code wait, and the
// exchange. openURL receives the link for the surface to launch (or
// display); the token endpoint is overridable for tests.
func Login(ctx context.Context, openURL func(string), tokenEndpoint string) (Token, error) {
	return LoginOnPort(ctx, CallbackPort, openURL, tokenEndpoint)
}

// LoginOnPort is Login with an explicit callback port (0 = ephemeral,
// for tests).
func LoginOnPort(ctx context.Context, port int, openURL func(string), tokenEndpoint string) (Token, error) {
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	p, err := NewPKCE()
	if err != nil {
		return Token{}, err
	}
	code, port, err := waitCallback(ctx, p.Verifier, port)
	if err != nil {
		return Token{}, err
	}
	link := AuthorizeLink(p, port)
	if openURL != nil {
		openURL(link)
	}
	var authCode string
	select {
	case authCode = <-code:
	case <-ctx.Done():
		return Token{}, errors.New("login timed out waiting for the browser")
	}
	if authCode == "" {
		return Token{}, errors.New("callback carried no authorization code")
	}
	return Exchange(ctx, tokenEndpoint, authCode, p.Verifier, p.Verifier, redirectURI(port))
}

// listen binds host:port (port 0 picks an ephemeral port for tests).
func listen(ctx context.Context, host string, port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, err
	}
	return ln, nil
}

// waitCallback starts the local server and returns the code channel plus
// the bound port (0 picks an ephemeral one; the built URL must match).
func waitCallback(ctx context.Context, state string, port int) (<-chan string, int, error) {
	mux := http.NewServeMux()
	codeCh := make(chan string, 1)
	server := &http.Server{Handler: mux}
	mux.HandleFunc(CallbackPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		fmt.Fprint(w, "<html><body><p>登录成功，可以关闭此页回到 Zgo。</p></body></html>")
		select {
		case codeCh <- code:
		default:
		}
	})
	ln, err := listen(ctx, CallbackHost, port)
	if err != nil {
		return nil, 0, fmt.Errorf("starting callback server: %w", err)
	}
	go server.Serve(ln)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()
	return codeCh, ln.Addr().(*net.TCPAddr).Port, nil
}

// Exchange trades the authorization code for tokens (pi's
// exchangeAuthorizationCode; expires with pi's five-minute skew).
func Exchange(ctx context.Context, tokenEndpoint, code, state, verifier, redirect string) (Token, error) {
	body := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     ClientID,
		"code":          code,
		"state":         state,
		"redirect_uri":  redirect,
		"code_verifier": verifier,
	}
	return postToken(ctx, tokenEndpoint, body)
}

// Refresh renews an access token with the refresh grant.
func Refresh(ctx context.Context, tokenEndpoint, refreshToken string) (Token, error) {
	body := map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     ClientID,
		"refresh_token": refreshToken,
	}
	return postToken(ctx, tokenEndpoint, body)
}

// postToken runs one token-endpoint POST and decodes the response.
func postToken(ctx context.Context, endpoint string, body map[string]string) (Token, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return Token{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("token endpoint returned %s", resp.Status)
	}
	var data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return Token{}, fmt.Errorf("parsing token response: %w", err)
	}
	if data.AccessToken == "" {
		return Token{}, errors.New("token response carried no access_token")
	}
	refresh := data.RefreshToken
	if refresh == "" {
		refresh = body["refresh_token"] // 刷新响应可不带回新的 refresh token
	}
	return Token{
		AccessToken:  data.AccessToken,
		RefreshToken: refresh,
		ExpiresAt:    time.Now().Add(time.Duration(data.ExpiresIn)*time.Second - 5*time.Minute),
	}, nil
}
