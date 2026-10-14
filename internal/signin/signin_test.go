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
