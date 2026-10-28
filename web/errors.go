package web

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// handlerFunc is an http.HandlerFunc that returns its failure instead of
// writing it. NewHandlerFunc turns the error into the response.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// HTTPError carries the status and safe public message a handler wants
// written to the browser, plus the internal cause used for logging. The
// cause is never written to the response.
type HTTPError struct {
	Status  int
	Message string
	Err     error
}

// NewHTTPError builds an HTTPError. err may be nil when there is no
// underlying cause.
func NewHTTPError(status int, message string, err error) *HTTPError {
	return &HTTPError{Status: status, Message: message, Err: err}
}

func (e *HTTPError) Error() string {
	if e.Err == nil {
		return e.Message
	}
	return e.Message + ": " + e.Err.Error()
}

func (e *HTTPError) Unwrap() error { return e.Err }

// NewHandlerFunc adapts f to http.HandlerFunc. An HTTPError answers with its
// status and safe message; 5xx ones are logged with their cause first. Any
// other error is logged and answered with a generic 500.
func NewHandlerFunc(f handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := f(w, r); err != nil {
			writeHandlerError(w, r, err)
		}
	}
}

func writeHandlerError(w http.ResponseWriter, r *http.Request, err error) {
	httpErr, ok := errors.AsType[*HTTPError](err)
	if !ok {
		slog.Error("request failed", "route", routePattern(r), "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if httpErr.Status >= http.StatusInternalServerError {
		slog.Error("request failed", "status", httpErr.Status, "route", routePattern(r), "err", httpErr.Err)
	}
	http.Error(w, httpErr.Message, httpErr.Status)
}

func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		return rc.RoutePattern()
	}
	return ""
}
