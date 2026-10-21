package signin

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// TestFlow: login, callback with the PKCE verifier, the session's bearer reaching the API, the
// JSON rule for cookie writes, and logout.
func TestFlow(t *testing.T) {
	idToken := "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"preferred_username":"alice"}`)) + ".x"
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "the-code" || r.Form.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok-alice","token_type":"Bearer","expires_in":300,"id_token":"` + idToken + `"}`))
	}))
	defer idp.Close()
	s := &Signin{OAuth: oauth2.Config{ClientID: "c", RedirectURL: "http://hareno/auth/callback",
		Endpoint: oauth2.Endpoint{AuthURL: "http://kiban/auth", TokenURL: idp.URL}}, Logout: "http://kiban/logout", Home: "http://hareno/"}

	w := httptest.NewRecorder()
	s.Login(w, httptest.NewRequest("GET", "/auth/login?user=alice", nil))
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Query().Get("code_challenge_method") != "S256" || loc.Query().Get("login_hint") != "alice" {
		t.Fatalf("no PKCE or login hint in %s", loc)
	}
	login := w.Result().Cookies()[0]

	// a callback whose state does not match the login cookie is refused
	r := httptest.NewRequest("GET", "/auth/callback?code=the-code&state=forged", nil)
	r.AddCookie(login)
	w = httptest.NewRecorder()
	s.Callback(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("forged state: %d", w.Code)
	}

	r = httptest.NewRequest("GET", "/auth/callback?code=the-code&state="+loc.Query().Get("state"), nil)
	r.AddCookie(login)
	w = httptest.NewRecorder()
	s.Callback(w, r)
	var sess *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			sess = c
		}
	}
	if w.Code != http.StatusFound || sess == nil || !sess.HttpOnly {
		t.Fatalf("callback: %d %v", w.Code, w.Result().Cookies())
	}

	var got string
	api := s.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Get("Authorization") }))
	call := func(method, ctype string, c *http.Cookie) int {
		got = ""
		r := httptest.NewRequest(method, "/api/sql", strings.NewReader("{}"))
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w.Code
	}
	if call("POST", "application/json", sess); got != "Bearer tok-alice" {
		t.Errorf("bearer: %q", got)
	}
	if code := call("POST", "application/x-www-form-urlencoded", sess); code != http.StatusUnsupportedMediaType || got != "" {
		t.Errorf("form post with the cookie: %d %q", code, got)
	}
	if call("POST", "application/json", &http.Cookie{Name: sessionCookie, Value: "guess"}); got != "" {
		t.Errorf("unknown session got %q", got)
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/api/me", nil)
	r.AddCookie(sess)
	s.Me(w, r)
	if !strings.Contains(w.Body.String(), `"alice"`) {
		t.Errorf("me: %s", w.Body)
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/auth/logout", nil)
	r.AddCookie(sess)
	s.LogoutHandler(w, r)
	if !strings.Contains(w.Body.String(), "id_token_hint") {
		t.Errorf("logout: %s", w.Body)
	}
	if call("POST", "application/json", sess); got != "" {
		t.Errorf("session still works after logout")
	}
}

// TestEmbedded: the sign-in modes of a dashboard embedded in another site. A silent login asks
// Kiban not to show a page and comes back marked either way; a popup login ends in a page that
// tells its opener; return goes back to a Hareno page, never to another site.
func TestEmbedded(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":300}`))
	}))
	defer idp.Close()
	s := &Signin{OAuth: oauth2.Config{ClientID: "c", Endpoint: oauth2.Endpoint{AuthURL: "http://kiban/auth", TokenURL: idp.URL}}, Home: "http://hareno/"}
	// login starts a login and returns what the callback answers, given Kiban's reply (code or error)
	login := func(query, reply string) (*url.URL, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		s.Login(w, httptest.NewRequest("GET", "/auth/login?"+query, nil))
		loc, _ := url.Parse(w.Header().Get("Location"))
		r := httptest.NewRequest("GET", "/auth/callback?"+reply+"&state="+loc.Query().Get("state"), nil)
		r.AddCookie(w.Result().Cookies()[0])
		w = httptest.NewRecorder()
		s.Callback(w, r)
		return loc, w
	}
	ret := url.QueryEscape("#/embed/abc?theme=dark")

	loc, w := login("silent=1&return="+ret, "error=login_required")
	if loc.Query().Get("prompt") != "none" {
		t.Errorf("silent login without prompt=none: %s", loc)
	}
	if got := w.Header().Get("Location"); w.Code != http.StatusFound || got != "http://hareno/#/embed/abc?theme=dark&signin=needed" {
		t.Errorf("silent login refused: %d %q", w.Code, got)
	}
	if _, w = login("silent=1&return="+ret, "code=x"); w.Header().Get("Location") != "http://hareno/#/embed/abc?theme=dark&signin=done" {
		t.Errorf("silent login done: %q", w.Header().Get("Location"))
	}
	if loc, w = login("popup=1", "code=x"); loc.Query().Get("prompt") != "" || w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "postMessage('hareno:signed-in'") {
		t.Errorf("popup login: %d %s", w.Code, w.Body)
	}
	if _, w = login("return="+url.QueryEscape("https://evil.example/"), "code=x"); w.Header().Get("Location") != "http://hareno/" {
		t.Errorf("return to another site: %q", w.Header().Get("Location"))
	}
	if _, w = login("return="+ret, "error=access_denied"); w.Code != http.StatusBadGateway {
		t.Errorf("an ordinary login that Kiban refuses: %d", w.Code)
	}
}
