package siwc

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	authorizeURL    = "https://auth.openai.com/api/accounts/authorize"
	tokenURL        = "https://auth.openai.com/api/accounts/oauth/token"
	resource        = "https://api.openai.com/v1"
	requestedScopes = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
)

var authClient = &http.Client{Timeout: 25 * time.Second}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
}

func tokenRequest(ctx context.Context, params url.Values) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := authClient.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return tokenResponse{}, fmt.Errorf("OpenAI token exchange returned HTTP %d", resp.StatusCode)
	}
	var t tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t); err != nil {
		return t, err
	}
	if t.AccessToken == "" {
		return t, errors.New("OpenAI returned no access token")
	}
	return t, nil
}

func hasScope(scopes, name string) bool {
	for _, s := range strings.Fields(scopes) {
		if s == name {
			return true
		}
	}
	return false
}

func verifyIDToken(ctx context.Context, raw, clientID, nonce string) (string, string, error) {
	if raw == "" {
		return "", "", errors.New("missing ID token")
	}
	var config struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := getJSON(ctx, "https://auth.openai.com/.well-known/openid-configuration", &config); err != nil {
		return "", "", err
	}
	if config.Issuer != "https://auth.openai.com" || !strings.HasPrefix(config.JWKSURI, "https://auth.openai.com/") {
		return "", "", errors.New("unexpected OpenAI OIDC metadata")
	}
	var keys struct {
		Keys []struct {
			KID string `json:"kid"`
			KTY string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := getJSON(ctx, config.JWKSURI, &keys); err != nil {
		return "", "", err
	}
	claims := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		for _, k := range keys.Keys {
			if kid == "" || k.KID != kid || k.KTY != "RSA" {
				continue
			}
			n, e := new(big.Int), new(big.Int)
			bn, errN := base64.RawURLEncoding.DecodeString(k.N)
			be, errE := base64.RawURLEncoding.DecodeString(k.E)
			if errN != nil || errE != nil {
				return nil, errors.New("invalid OpenAI JWKS key")
			}
			n.SetBytes(bn)
			e.SetBytes(be)
			if n.Sign() <= 0 || !e.IsInt64() || e.Int64() < 3 {
				return nil, errors.New("invalid RSA key")
			}
			return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
		}
		return nil, errors.New("OpenAI JWKS key not found")
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(config.Issuer), jwt.WithAudience(clientID), jwt.WithExpirationRequired())
	if err != nil || !tok.Valid {
		return "", "", fmt.Errorf("ID token verification failed: %w", err)
	}
	if claims["nonce"] != nonce {
		return "", "", errors.New("ID token nonce mismatch")
	}
	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	if sub == "" {
		return "", "", errors.New("ID token has no subject")
	}
	return sub, email, nil
}

func getJSON(ctx context.Context, endpoint string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := authClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("OpenAI identity metadata returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(into)
}

// Login runs a fresh, user-consented loopback PKCE/OIDC authorization. openBrowser
// must use the system browser; callers must not log the authorization URL.
func (st *Store) Login(ctx context.Context, openBrowser func(string) error) error {
	current := st.Snapshot()
	state, err := randomToken(32)
	if err != nil {
		return err
	}
	nonce, err := randomToken(32)
	if err != nil {
		return err
	}
	verifier, err := randomToken(32)
	if err != nil {
		return err
	}
	challenge := sha256.Sum256([]byte(verifier))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	redirect := fmt.Sprintf("http://127.0.0.1:%d/auth/callback", listener.Addr().(*net.TCPAddr).Port)
	clientID := current.ClientID
	newRegistration := clientID == ""
	if newRegistration {
		clientID = "dynamic_agent_client"
	}
	q := url.Values{
		"client_id": {clientID}, "response_type": {"code"}, "redirect_uri": {redirect},
		"scope": {requestedScopes}, "resource": {resource}, "state": {state}, "nonce": {nonce},
		"code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"ext_agent_host_id": {current.HostID},
	}
	if newRegistration {
		q.Set("agent_name_hint", "ARTEX")
	}
	type result struct {
		code, clientID string
		err            error
	}
	got := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid state", 400)
			return
		}
		res := result{code: r.URL.Query().Get("code"), clientID: r.URL.Query().Get("client_id")}
		if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
			res.err = fmt.Errorf("OpenAI authorization declined: %s", oauthErr)
		}
		if res.code == "" && res.err == nil {
			res.err = errors.New("authorization callback has no code")
		}
		select {
		case got <- res:
		default:
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ARTEX ChatGPT 로그인 결과를 확인했습니다. 이 창을 닫아도 됩니다.")
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	defer srv.Shutdown(context.Background())
	if err := openBrowser(authorizeURL + "?" + q.Encode()); err != nil {
		return err
	}
	var callback result
	select {
	case callback = <-got:
	case <-ctx.Done():
		return ctx.Err()
	}
	if callback.err != nil {
		return callback.err
	}
	if newRegistration {
		if callback.clientID == "" || callback.clientID == "dynamic_agent_client" {
			return errors.New("OpenAI did not issue a client ID")
		}
		clientID = callback.clientID
		// Preserve registration if a later token exchange must be retried.
		if err := st.Update(func(s *Session) error { s.ClientID = clientID; return nil }); err != nil {
			return err
		}
	} else if callback.clientID != "" && callback.clientID != clientID {
		return errors.New("OpenAI returned a different client ID")
	}
	t, err := tokenRequest(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {callback.code}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "resource": {resource}})
	if err != nil {
		return err
	}
	sub, email, err := verifyIDToken(ctx, t.IDToken, clientID, nonce)
	if err != nil {
		return err
	}
	if current.Subject != "" && current.Subject != sub {
		return errors.New("different ChatGPT account; disconnect the existing account first")
	}
	if !hasScope(t.Scope, "chatgpt.tokens.use.direct") {
		return errors.New("ChatGPT plan use was not approved")
	}
	return st.Update(func(s *Session) error {
		s.ClientID, s.Subject, s.Email = clientID, sub, email
		s.IDToken, s.AccessToken, s.RefreshToken = t.IDToken, t.AccessToken, t.RefreshToken
		s.Scopes, s.ExpiresAt = t.Scope, time.Now().UTC().Add(time.Duration(t.ExpiresIn)*time.Second)
		return nil
	})
}

func (st *Store) Access(ctx context.Context) (string, error) {
	if err := st.Reload(); err != nil {
		return "", err
	}
	current := st.Snapshot()
	if current.AccessToken == "" || !hasScope(current.Scopes, "chatgpt.tokens.use.direct") {
		return "", errors.New("ChatGPT plan not connected; run login")
	}
	if time.Until(current.ExpiresAt) > 2*time.Minute {
		return current.AccessToken, nil
	}
	var access string
	err := st.Update(func(s *Session) error {
		if s.RefreshToken == "" {
			return errors.New("ChatGPT session expired; sign in again")
		}
		t, err := tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {s.ClientID}, "refresh_token": {s.RefreshToken}, "resource": {resource}})
		if err != nil {
			return err
		}
		if t.Scope != "" && !hasScope(t.Scope, "chatgpt.tokens.use.direct") {
			return errors.New("ChatGPT plan permission was removed")
		}
		s.AccessToken = t.AccessToken
		if t.Scope != "" {
			s.Scopes = t.Scope
		}
		if t.RefreshToken != "" {
			s.RefreshToken = t.RefreshToken
		}
		s.ExpiresAt = time.Now().UTC().Add(time.Duration(t.ExpiresIn) * time.Second)
		access = s.AccessToken
		return nil
	})
	return access, err
}

// Logout revokes the renewable session and clears local credentials. The
// registration is retained for a later sign-in to the same account.
func (st *Store) Logout(ctx context.Context) error {
	if err := st.Reload(); err != nil {
		return err
	}
	s := st.Snapshot()
	var revokeErr error
	if s.RefreshToken != "" {
		var config struct {
			RevocationEndpoint string `json:"revocation_endpoint"`
		}
		if err := getJSON(ctx, "https://auth.openai.com/.well-known/openid-configuration", &config); err != nil {
			revokeErr = err
		} else if !strings.HasPrefix(config.RevocationEndpoint, "https://auth.openai.com/") {
			revokeErr = errors.New("unexpected revocation endpoint")
		} else {
			values := url.Values{"token": {s.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {s.ClientID}}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.RevocationEndpoint, strings.NewReader(values.Encode()))
			if err != nil {
				revokeErr = err
			} else {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				resp, err := authClient.Do(req)
				if err != nil {
					revokeErr = err
				} else {
					resp.Body.Close()
					if resp.StatusCode != 200 {
						revokeErr = fmt.Errorf("OpenAI revocation returned HTTP %d", resp.StatusCode)
					}
				}
			}
		}
	}
	if err := st.Update(func(s *Session) error {
		s.IDToken, s.AccessToken, s.RefreshToken, s.Scopes = "", "", "", ""
		s.ExpiresAt = time.Time{}
		return nil
	}); err != nil {
		return err
	}
	if revokeErr != nil {
		return fmt.Errorf("local sign-out complete, remote revocation unconfirmed: %w", revokeErr)
	}
	return nil
}
