package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The management page is the one part of the service that is not read-only, so
// it is the part a token can close. These tests pin the boundary: with a token
// set only a caller that presents it reaches the page, and with no token the
// page behaves exactly as it did before the token existed.

const testToken = "correct-horse-battery-staple"

func doDecorated(t *testing.T, s *Server, method, target string, decorate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if decorate != nil {
		decorate(req)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestManagePageRequiresTheToken(t *testing.T) {
	// Admin is nil here, so a request that clears the gate answers 404 rather
	// than rendering the page. That keeps these cases about authorization
	// alone: 401 is the gate refusing, 404 is the gate letting the request
	// through to a server with no management store wired up.
	s := New(Options{ManageToken: testToken})
	cases := []struct {
		name     string
		decorate func(*http.Request)
		want     int
	}{
		{"no credentials", nil, http.StatusUnauthorized},
		{"wrong basic password", func(r *http.Request) { r.SetBasicAuth("feedme", "wrong") }, http.StatusUnauthorized},
		{"empty basic password", func(r *http.Request) { r.SetBasicAuth("feedme", "") }, http.StatusUnauthorized},
		{"wrong bearer token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, http.StatusUnauthorized},
		{"basic, username ignored", func(r *http.Request) { r.SetBasicAuth("anything", testToken) }, http.StatusNotFound},
		{"bearer token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testToken) }, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doDecorated(t, s, http.MethodGet, "/feeds", tc.decorate)
			if rec.Code != tc.want {
				t.Fatalf("GET /feeds = %d, want %d", rec.Code, tc.want)
			}
			if tc.want == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("401 without WWW-Authenticate: a browser would show a blank page instead of asking")
			}
		})
	}
}

func TestManagePageMutationsAreGatedToo(t *testing.T) {
	s := New(Options{ManageToken: testToken})
	if rec := doDecorated(t, s, http.MethodPost, "/feeds", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /feeds without a token = %d, want 401", rec.Code)
	}
}

func TestManagePageIsOpenWithoutAToken(t *testing.T) {
	s := New(Options{})
	rec := doDecorated(t, s, http.MethodGet, "/feeds", nil)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("an instance with no token configured must not challenge")
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("no token configured, but a challenge was advertised: %q", got)
	}
}

func TestTokenLeavesTheBuilderPublic(t *testing.T) {
	s := New(Options{ManageToken: testToken})
	rec := doDecorated(t, s, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200: the token guards management, not the builder", rec.Code)
	}
}
