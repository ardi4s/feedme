package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"strings"
)

// The management page is the one part of the service that is not read-only, so
// it is the part a token can close. Closing it by answering 401 and letting the
// browser ask for HTTP Basic credentials no longer works reliably: Chromium
// sometimes answers a top-level navigation with an error page instead of
// showing the prompt, which locks an operator out of their own server with no
// way back in from the browser.
//
// So a browser is given a form at /login, and the POST behind it exchanges the
// token for a cookie the management page accepts. The token itself is still
// accepted as a Basic password and as a bearer token, because that is what a
// script sends and what the command line documents.

const (
	feedsPath  = "/feeds"
	opmlPath   = "/feeds/opml"
	exportPath = "/feeds/export"
	loginPath  = "/login"

	// sessionCookieName is the cookie the sign-in form sets.
	sessionCookieName = "feedme_session"

	// sessionCookieMsg salts the derived cookie value, so that a cookie read
	// out of a browser is not the string that is also typed at the command
	// line.
	sessionCookieMsg = "feedme-session-v1"
)

// handleLogin serves the sign-in form and consumes it.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.manageToken == "" {
		// There is nothing to sign in to, because the page is already open.
		// Send the caller on rather than to a form that cannot help.
		http.Redirect(w, r, feedsPath, http.StatusSeeOther)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.renderLogin(w, r, "")
	case http.MethodPost:
		s.submitLogin(w, r)
	default:
		s.fail(w, r, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// submitLogin checks the posted token and, when it matches, sets the session
// cookie and hands the caller back to the management page.
//
// A rejected token re-renders the form with a message rather than answering
// 401, because a 401 is what a browser turns into an error page in this
// situation — the very thing the form exists to avoid. The status code a script
// wants is still returned by the gate on the paths the gate protects.
func (s *Server) submitLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "bad form: "+err.Error())
		return
	}
	if !tokenEqual(strings.TrimSpace(r.PostFormValue("token")), s.manageToken) {
		s.renderLogin(w, r, "That is not the token this server was started with.")
		return
	}
	http.SetCookie(w, s.sessionCookie(r))
	http.Redirect(w, r, feedsPath, http.StatusSeeOther)
}

// renderLogin writes the form. message explains a rejected attempt, and is
// empty on the first visit.
func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, loginPage(message))
}

// sessionCookie is the cookie the sign-in form sets.
//
// It is a session cookie: no expiry, so closing the browser ends the session
// and the token in the environment stays the only durable secret. SameSite=Lax
// still lets a link from elsewhere open the page, while keeping the cookie off
// the cross-site form posts that would otherwise be a route into the
// management actions.
func (s *Server) sessionCookie(r *http.Request) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    s.sessionValue(),
		Path:     feedsPath,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestScheme(r) == "https",
	}
}

// sessionValue derives the cookie value from the token rather than storing the
// token itself, so a leaked cookie does not hand over the password, and so
// changing the token quietly ends every session opened with the old one.
func (s *Server) sessionValue() string {
	mac := hmac.New(sha256.New, []byte(s.manageToken))
	mac.Write([]byte(sessionCookieMsg))
	return hex.EncodeToString(mac.Sum(nil))
}

// credentialOK reports whether the caller presented the token, as the password
// of a Basic pair, as a bearer token, or as the session cookie.
func (s *Server) credentialOK(r *http.Request) bool {
	if _, pass, ok := r.BasicAuth(); ok && tokenEqual(pass, s.manageToken) {
		return true
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if tokenEqual(strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")), s.manageToken) {
			return true
		}
	}
	if c, err := r.Cookie(sessionCookieName); err == nil && tokenEqual(c.Value, s.sessionValue()) {
		return true
	}
	return false
}

// tokenEqual compares two secrets without leaking them through how long the
// comparison takes.
func tokenEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// wantsHTML reports whether the caller is a browser navigating to a page rather
// than a script asking for an answer. Only browsers are sent to the sign-in
// form; everything else keeps getting the challenge it knows how to answer.
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// loginPage is the sign-in form. It is built from the same stylesheet and the
// same header as the management page it leads to, so the two read as one
// application.
func loginPage(message string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>feedme - sign in</title>` + iconLinksHTML)
	b.WriteString(`<style>` + themeCSS + `</style></head><body><main>`)
	b.WriteString(brandHeader(navNewFeed))
	b.WriteString(`<div class="login"><div class="card">`)
	b.WriteString(`<h1>Manage feeds</h1>`)
	b.WriteString(`<p class="lede">This server keeps its management page behind a token. ` +
		`Enter it to see the feeds that have been built.</p>`)
	if message != "" {
		b.WriteString(`<p class="err" role="alert">` + html.EscapeString(message) + `</p>`)
	}
	b.WriteString(`<form method="post" action="/login">`)
	b.WriteString(`<label for="token">Token</label>`)
	b.WriteString(`<input id="token" name="token" type="password" autocomplete="current-password" autofocus required>`)
	b.WriteString(`<button class="btn primary" type="submit">Sign in</button>`)
	b.WriteString(`</form>`)
	b.WriteString(`<p class="note">The token is the value of <code>FEEDME_ADMIN_TOKEN</code>, ` +
		`or of <code>-admin-token</code> when the server was started.</p>`)
	b.WriteString(`</div></div></main></body></html>`)
	return b.String()
}
