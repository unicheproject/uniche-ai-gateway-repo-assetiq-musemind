package web

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DevTokenProxy exchanges a username and password for a Keycloak access token
// on behalf of Swagger UI's OAuth2 password flow. The browser cannot call
// Keycloak directly (no CORS origin on the dev client), so it posts to this
// same-origin endpoint instead. Dev only: it sees users' passwords.
func DevTokenProxy(tokenURL, clientID string) http.HandlerFunc {
	client := &http.Client{Timeout: 10 * time.Second}
	return NewHandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		if err := r.ParseForm(); err != nil {
			return NewHTTPError(http.StatusBadRequest, "invalid form", err)
		}
		form := url.Values{
			"grant_type": {"password"},
			"client_id":  {clientID},
			"username":   {r.PostForm.Get("username")},
			"password":   {r.PostForm.Get("password")},
		}
		if scope := r.PostForm.Get("scope"); scope != "" {
			form.Set("scope", scope)
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
		if err != nil {
			return NewHTTPError(http.StatusInternalServerError, "internal error", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := client.Do(req)
		if err != nil {
			return NewHTTPError(http.StatusBadGateway, "identity provider unreachable", err)
		}
		defer resp.Body.Close()

		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return nil
	})
}
