// Package auth handles Google authentication. Two flavors are supported and
// auto-detected from the credentials file:
//
//   - an installed-app OAuth client (client_secrets.json), driven by the
//     loopback flow in `ga4 auth login`; tokens are stored in the OS keychain
//     (via zalando/go-keyring) when available, with a fallback to
//     <config-dir>/ga4/token.json (mode 0600).
//   - a service account key file, which signs its own tokens and needs no
//     interactive login.
//
// See credentials.go for the shared entry point both flavors resolve through.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/KLIXPERT-io/ga4-cli/internal/config"
	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	analyticsdata "google.golang.org/api/analyticsdata/v1beta"
)

const (
	keyringService = "ga4-cli"
	keyringAccount = "default"
)

// Scopes required by the CLI. analytics.readonly covers both the Data API
// (reporting) and the Admin API reads (accounts, properties, data streams).
// The CLI never writes to Google Analytics, so no edit scope is requested.
var Scopes = []string{analyticsdata.AnalyticsReadonlyScope}

// ClientSecrets represents the installed-app OAuth client json.
type ClientSecrets struct {
	Installed struct {
		ClientID     string   `json:"client_id"`
		ClientSecret string   `json:"client_secret"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthURI      string   `json:"auth_uri"`
		TokenURI     string   `json:"token_uri"`
	} `json:"installed"`
}

// LoadConfig reads a client_secrets.json file and returns an OAuth2 config.
func LoadConfig(credentialsPath string) (*oauth2.Config, error) {
	if credentialsPath == "" {
		return nil, ErrNoCredentials
	}
	b, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	cfg, err := google.ConfigFromJSON(b, Scopes...)
	if err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	return cfg, nil
}

// Login runs the OAuth loopback flow and stores the resulting token.
func Login(ctx context.Context, cfg *oauth2.Config, openBrowser func(url string) error) (*oauth2.Token, error) {
	// Bind a random local port; the redirect URI is derived from it, so no
	// fixed port has to be pre-registered on the OAuth client.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer lis.Close()
	redirect := fmt.Sprintf("http://%s/callback", lis.Addr().String())
	cfg.RedirectURL = redirect

	state, err := randomString(24)
	if err != nil {
		return nil, err
	}
	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	fmt.Fprintln(os.Stderr, "Open this URL in your browser to authorize:")
	fmt.Fprintln(os.Stderr, authURL)
	if openBrowser != nil {
		_ = openBrowser(authURL)
	}

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q, _ := url.ParseQuery(r.URL.RawQuery)
			if q.Get("state") != state {
				http.Error(w, "state mismatch", http.StatusBadRequest)
				errCh <- errors.New("oauth state mismatch")
				return
			}
			if e := q.Get("error"); e != "" {
				http.Error(w, e, http.StatusBadRequest)
				errCh <- fmt.Errorf("oauth denied: %s", e)
				return
			}
			fmt.Fprintln(w, "Authorization complete. You can close this tab.")
			codeCh <- q.Get("code")
		}),
	}
	go srv.Serve(lis)
	defer srv.Shutdown(context.Background())

	var code string
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errCh:
		return nil, err
	case code = <-codeCh:
	case <-time.After(5 * time.Minute):
		return nil, errors.New("timeout waiting for authorization")
	}
	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}
	if err := SaveToken(tok); err != nil {
		return nil, err
	}
	return tok, nil
}

// TokenSource returns an auto-refreshing token source wired to the loaded token.
func TokenSource(ctx context.Context, cfg *oauth2.Config) (oauth2.TokenSource, *oauth2.Token, error) {
	tok, err := LoadToken()
	if err != nil {
		return nil, nil, err
	}
	ts := cfg.TokenSource(ctx, tok)
	// Refresh once up front so an expired grant surfaces here rather than as a
	// confusing 401 in the middle of a report.
	refreshed, err := ts.Token()
	if err != nil {
		return nil, nil, fmt.Errorf("refresh token: %w", err)
	}
	if refreshed.AccessToken != tok.AccessToken {
		_ = SaveToken(refreshed)
	}
	return ts, refreshed, nil
}

// HTTPClient returns an authenticated http.Client.
func HTTPClient(ctx context.Context, cfg *oauth2.Config) (*http.Client, error) {
	ts, _, err := TokenSource(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return oauth2.NewClient(ctx, ts), nil
}

// ===== Token storage =====

func tokenFilePath() (string, error) {
	d, err := config.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "token.json"), nil
}

// SaveToken stores the token in the OS keychain (with file fallback).
func SaveToken(tok *oauth2.Token) error {
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	if err := keyring.Set(keyringService, keyringAccount, string(b)); err == nil {
		return nil
	}
	p, err := tokenFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// LoadToken reads the token, preferring the keychain, falling back to the file.
func LoadToken() (*oauth2.Token, error) {
	if s, err := keyring.Get(keyringService, keyringAccount); err == nil && s != "" {
		var tok oauth2.Token
		if err := json.Unmarshal([]byte(s), &tok); err == nil {
			return &tok, nil
		}
	}
	p, err := tokenFilePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoToken
		}
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(b, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// DeleteToken removes any stored tokens.
func DeleteToken() error {
	_ = keyring.Delete(keyringService, keyringAccount)
	p, err := tokenFilePath()
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ErrNoToken indicates no stored token was found.
var ErrNoToken = errors.New("no token stored; run `ga4 auth login`")

// Identity returns a stable identifier for the current token (for cache
// keying): the client_id plus a short prefix of the refresh token.
func Identity(cfg *oauth2.Config, tok *oauth2.Token) string {
	id := cfg.ClientID
	if tok != nil && tok.RefreshToken != "" {
		id += ":" + tok.RefreshToken[:min(8, len(tok.RefreshToken))]
	}
	return id
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
