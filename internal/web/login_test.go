package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A browser is never shown the Basic prompt any more, because Chromium stopped
// answering it reliably and renders an error page instead. It gets a form. These
// tests pin that: the HTML path leads to /login and advertises no challenge, the
// form exchanges the token for a session cookie, and that cookie opens exactly
// the page the token would.

const browserAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

// browserRequest sends what a browser sends: an Accept header that leads with
// HTML, and a form body for a POST.
func browserRequest(t *testing.T, s *Server, method, target, body string, decorate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Header.Set("Accept", browserAccept)
	if decorate != nil {
		decorate(r)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, r)
	return rec
}

// signIn walks the form the way a browser does and returns the cookie it set.
func signIn(t *testing.T, s *Server, token string) *http.Cookie {
	t.Helper()
	rec := browserRequest(t, s, http.MethodPost, loginPath, url.Values{"token": {token}}.Encode(), nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST %s = %d, want 303", loginPath, rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("signing in set %d cookies, want 1", len(cookies))
	}
	return cookies[0]
}

func TestBrowserIsSentToTheSignInForm(t *testing.T) {
	s := New(Options{ManageToken: testToken})
	rec := browserRequest(t, s, http.MethodGet, feedsPath, "", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET %s from a browser = %d, want 303", feedsPath, rec.Code)
	}
	if got := rec.Header().Get("Location"); got != loginPath {
		t.Errorf("Location = %q, want %q", got, loginPath)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("a browser was challenged with %q: the form exists to replace that prompt", got)
	}
}

func TestSignInPageIsNotAChallenge(t *testing.T) {
	s := New(Options{ManageToken: testToken})
	rec := browserRequest(t, s, http.MethodGet, loginPath, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s without credentials = %d, want 200", loginPath, rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("the sign-in page advertised %q", got)
	}
	body := rec.Body.String()
	// The page is meant to read as part of the same application, so it carries
	// the shared header and the shared stylesheet rather than its own markup.
	for _, want := range []string{`action="/login"`, `type="password"`, `name="token"`, `class="login"`, `class="brand"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the sign-in page is missing %s", want)
		}
	}
}

func TestSignInCookieOpensTheManagementPage(t *testing.T) {
	s := New(Options{ManageToken: testToken})
	cookie := signIn(t, s, testToken)

	if cookie.Value == testToken {
		t.Error("the cookie carries the token itself, want a value derived from it")
	}
	if !cookie.HttpOnly {
		t.Error("the session cookie is readable from JavaScript")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != feedsPath {
		t.Errorf("Path = %q, want %q: the cookie is for the management page alone", cookie.Path, feedsPath)
	}
	if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Error("the cookie outlives the browser session")
	}

	// Admin is nil here, so a request that clears the gate answers 404 rather
	// than rendering the page. 404 is therefore the sign that the cookie was
	// accepted, where a refusal would have been a redirect to the form.
	rec := browserRequest(t, s, http.MethodGet, feedsPath, "", func(r *http.Request) {
		r.AddCookie(cookie)
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET %s with the session cookie = %d, want 404", feedsPath, rec.Code)
	}
}

func TestManagementActionsAcceptTheCookie(t *testing.T) {
	// The page's forms post to /feeds, so a session that opens the page has to
	// carry those posts too, or the page would be read-only.
	s := New(Options{ManageToken: testToken})
	cookie := signIn(t, s, testToken)
	rec := browserRequest(t, s, http.MethodPost, feedsPath, url.Values{"bulk": {"refresh"}}.Encode(), func(r *http.Request) {
		r.AddCookie(cookie)
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST %s with the session cookie = %d, want 404 (past the gate)", feedsPath, rec.Code)
	}
}

func TestSignInRejectsTheWrongToken(t *testing.T) {
	s := New(Options{ManageToken: testToken})
	for _, token := range []string{"", "wrong"} {
		rec := browserRequest(t, s, http.MethodPost, loginPath, url.Values{"token": {token}}.Encode(), nil)
		if got := rec.Result().Cookies(); len(got) != 0 {
			t.Fatalf("token %q was accepted", token)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("rejected sign in = %d, want 200 with the form: a 401 is what a browser turns into an error page", rec.Code)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != "" {
			t.Errorf("a rejected sign in advertised %q", got)
		}
		if !strings.Contains(rec.Body.String(), `class="err"`) {
			t.Error("a rejected sign in did not say why")
		}
	}
}

func TestSessionCookieDiesWithTheToken(t *testing.T) {
	// The cookie is derived from the token, so a changed token must quietly end
	// the sessions opened with the old one.
	cookie := signIn(t, New(Options{ManageToken: testToken}), testToken)

	rotated := New(Options{ManageToken: "a-different-token"})
	rec := browserRequest(t, rotated, http.MethodGet, feedsPath, "", func(r *http.Request) {
		r.AddCookie(cookie)
	})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != loginPath {
		t.Fatalf("a cookie from the old token got %d %q, want a redirect to %s",
			rec.Code, rec.Header().Get("Location"), loginPath)
	}
}

func TestSessionCookieIsSecureOnlyWhenTheCallerUsedHTTPS(t *testing.T) {
	s := New(Options{ManageToken: testToken})
	cases := []struct {
		name   string
		proto  string
		secure bool
	}{
		{"plain http", "", false},
		{"behind a TLS proxy", "https", true},
		{"proxy header with a list", "https, http", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := browserRequest(t, s, http.MethodPost, loginPath,
				url.Values{"token": {testToken}}.Encode(), func(r *http.Request) {
					if tc.proto != "" {
						r.Header.Set("X-Forwarded-Proto", tc.proto)
					}
				})
			cookies := rec.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("signing in set %d cookies, want 1", len(cookies))
			}
			if cookies[0].Secure != tc.secure {
				t.Errorf("Secure = %v, want %v", cookies[0].Secure, tc.secure)
			}
		})
	}
}

func TestSignInRedirectsWhenThereIsNoToken(t *testing.T) {
	// Without a token there is nothing to sign in to, so the form would be a
	// dead end. Send the caller to the page they asked for.
	s := New(Options{})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := browserRequest(t, s, method, loginPath, "token=anything", nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != feedsPath {
			t.Fatalf("%s %s with no token configured = %d %q, want a redirect to %s",
				method, loginPath, rec.Code, rec.Header().Get("Location"), feedsPath)
		}
	}
}

func TestSignInRejectsOtherMethods(t *testing.T) {
	s := New(Options{ManageToken: testToken})
	if rec := browserRequest(t, s, http.MethodPut, loginPath, "", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT %s = %d, want 405", loginPath, rec.Code)
	}
}
