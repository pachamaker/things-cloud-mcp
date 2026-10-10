package main

import (
	"errors"
	"net/url"
	"strings"
)

const redactedPath = "<redacted>"

// redactRequestURL hides the path and query of a Things Cloud request that
// failed below HTTP. net/http then returns a *url.Error carrying the full URL:
// history URLs contain the history key, which opens the task database without
// the password, and account URLs the e-mail address (KI-35). The operation, the
// host and the cause stay in the message. Apply it where SDK errors enter the
// server, before they are wrapped, logged or returned to a client.
func redactRequestURL(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) || ue.URL == "" {
		return err
	}
	safe := &url.Error{Op: ue.Op, URL: redactedURL(ue.URL), Err: ue.Err}
	return &redactedError{msg: strings.ReplaceAll(err.Error(), ue.URL, safe.URL), safe: safe}
}

func redactedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return redactedPath
	}
	return u.Scheme + "://" + u.Host + "/" + redactedPath
}

// redactedError unwraps to the redacted *url.Error, so errors.Is and
// errors.As still reach the cause but never the original URL. Wrappers
// between the original error and its *url.Error drop out of the chain.
type redactedError struct {
	msg  string
	safe *url.Error
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Unwrap() error { return e.safe }
