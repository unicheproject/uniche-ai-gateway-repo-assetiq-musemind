package eccch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenSource turns an EGI Check-in refresh token into access tokens, kept in
// memory only. Neither token is ever logged.
type TokenSource struct {
	tokenURL string
	clientID string
	http     *http.Client

	mu      sync.Mutex
	access  string
	refresh string
}

func NewTokenSource(tokenURL, clientID, refreshToken string) *TokenSource {
	return &TokenSource{
		tokenURL: tokenURL,
		clientID: clientID,
		http:     &http.Client{Timeout: 30 * time.Second},
		refresh:  refreshToken,
	}
}

// Token returns the current access token, refreshing first if there is none.
func (s *TokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.access == "" {
		if err := s.refreshLocked(ctx); err != nil {
			return "", err
		}
	}
	return s.access, nil
}

// Refresh returns an access token to use instead of stale, the one the server
// rejected. If a parallel caller already replaced stale, it returns that
// token without asking EGI again. A rotated refresh token replaces the old
// one in memory.
func (s *TokenSource) Refresh(ctx context.Context, stale string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.access != stale {
		return s.access, nil
	}
	if err := s.refreshLocked(ctx); err != nil {
		return "", err
	}
	return s.access, nil
}

func (s *TokenSource) refreshLocked(ctx context.Context) error {
	if s.refresh == "" {
		return errors.New("refreshing egi token: no refresh token; set EGI_REFRESH_TOKEN")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {s.refresh},
		"client_id":     {s.clientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("building egi token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("refreshing egi token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The body is an OAuth error, never a token, so it is safe to show.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("refreshing egi token: unexpected status %s: %s; get a new EGI_REFRESH_TOKEN from the token portal", resp.Status, msg)
	}

	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return fmt.Errorf("decoding egi token response: %w", err)
	}
	if tok.AccessToken == "" {
		return errors.New("refreshing egi token: response has no access_token")
	}
	s.access = tok.AccessToken
	if tok.RefreshToken != "" {
		s.refresh = tok.RefreshToken
	}
	return nil
}
