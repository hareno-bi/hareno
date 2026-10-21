// Package signin keeps the browser's Kiban login on the server (Kiban's "your backend holds the
// token" shape, docs/integrate.md): the browser gets an httpOnly session cookie and never sees a
// Kiban token. It also works on plain-HTTP dev origins, where the browser SDK cannot run PKCE
// (crypto.subtle needs a secure context).
//
//	GET  /auth/login     starts the authorization code flow with PKCE (?user= pre-fills the name;
//	                     ?return=#/... comes back to that page; ?silent=1 signs in only if Kiban
//	                     already knows the browser, without showing a page; ?popup=1 for a sign-in
//	                     window that closes itself: both for dashboards embedded in another site)
//	GET  /auth/callback  exchanges the code and sets the session cookie
//	POST /auth/logout    ends the session here and in Kiban
//	GET  /api/me         the signed-in user's display name, or null; and any test accounts
//
// Wrap puts the session's access token on API requests as Authorization: Bearer, refreshing it
// when it expires, so the API handlers stay token-based.
package signin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const (
	sessionCookie = "hareno_session"
	loginCookie   = "hareno_login"
	idle          = 12 * time.Hour // a session unused this long is dropped
)

// Signin is the login flow and the session store.
type Signin struct {
	OAuth  oauth2.Config // Endpoint.AuthURL is the browser's view of Kiban; TokenURL the server's
	Logout string        // Kiban's end-session URL, as the browser reaches it
	Home   string        // Hareno's public address, where the browser goes after login and logout
	Demo   []Account     // dev and test stacks only: accounts shown on the sign-in page, passwords included

	mu       sync.Mutex
	sessions map[string]*session // ponytail: in memory, so a restart signs everyone out and replicas do not share; move to Valkey with R1-5's cache
}

// Account is a test account listed on the sign-in page of a dev or demo stack, with the access
// the seed gives it (as documentation for testers: Kiban decides).
type Account struct {
	User      string   `json:"user" yaml:"user"`
	Password  string   `json:"password" yaml:"password"`
	Remarks   []string `json:"remarks" yaml:"remarks"`      // what the account is for, in short points
	Company   string   `json:"company" yaml:"company"`      // Kiban company code
	Via       string   `json:"via" yaml:"via"`              // the position or group their access comes from
	SQL       bool     `json:"sql" yaml:"sql"`              // may use the dashboard editor
	Rows      string   `json:"rows" yaml:"rows"`            // their row scope, e.g. "North"
	ShareData bool     `json:"shareData" yaml:"share_data"` // may share dashboards showing their data
	Export    bool     `json:"export" yaml:"export"`        // may download CSV
	Datasets  bool     `json:"datasets" yaml:"datasets"`    // may build dashboards from datasets (without SQL)
}

type session struct {
	src     oauth2.TokenSource // refreshes the access token with the refresh token
	idToken string
	name    string
	used    time.Time
}

// back is a page of Hareno's own to return to after login: a hash route, so never another site.
var back = regexp.MustCompile(`^#/[A-Za-z0-9/_.~%=&?+:-]*$`)

func (s *Signin) Login(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state, verifier := rand.Text(), oauth2.GenerateVerifier()
	mode, ret := "", q.Get("return")
	if q.Get("popup") == "1" {
		mode = "popup"
	} else if q.Get("silent") == "1" {
		mode = "silent"
	}
	if !back.MatchString(ret) {
		ret = ""
	}
	setCookie(w, r, loginCookie, strings.Join([]string{state, verifier, mode, base64.RawURLEncoding.EncodeToString([]byte(ret))}, "."), 600)
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	if u := q.Get("user"); u != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", u)) // pre-fills the user name on Kiban's page
	}
	if mode == "silent" {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "none")) // Kiban answers at once: signed in, or login_required
	}
	http.Redirect(w, r, s.OAuth.AuthCodeURL(state, opts...), http.StatusFound)
}

// mark adds key=value to a hash route's query: how a silent login tells the page what happened.
func mark(route, kv string) string {
	if route == "" {
		route = "#/"
	}
	if strings.Contains(route, "?") {
		return route + "&" + kv
	}
	return route + "?" + kv
}

func (s *Signin) Callback(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(loginCookie)
	parts := append(strings.SplitN(valueOf(c, err), ".", 4), "", "", "")
	state, verifier, mode := parts[0], parts[1], parts[2]
	ret, _ := base64.RawURLEncoding.DecodeString(parts[3])
	if state == "" || r.URL.Query().Get("state") != state {
		http.Error(w, "login expired or started elsewhere; start again from Hareno", http.StatusBadRequest)
		return
	}
	setCookie(w, r, loginCookie, "", -1)
	if r.URL.Query().Get("error") != "" {
		if mode == "silent" { // not signed in to Kiban: the page offers a sign-in button instead
			http.Redirect(w, r, s.Home+mark(string(ret), "signin=needed"), http.StatusFound)
			return
		}
		http.Error(w, "login failed", http.StatusBadGateway)
		return
	}
	tok, err := s.OAuth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		http.Error(w, "login failed", http.StatusBadGateway)
		return
	}
	idToken, _ := tok.Extra("id_token").(string)
	sess := &session{
		// background context: the source refreshes after this request has finished
		src:     oauth2.ReuseTokenSource(tok, s.OAuth.TokenSource(context.Background(), tok)),
		idToken: idToken,
		name:    displayName(idToken),
		used:    time.Now(),
	}
	id := rand.Text()
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]*session{}
	}
	s.sessions[id] = sess
	s.mu.Unlock()
	setCookie(w, r, sessionCookie, id, 0)
	switch mode {
	case "popup": // tell the page that opened this window, then close it
		home, _ := json.Marshal(s.Home)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Signed in to Hareno</title><p>Signed in. You can close this window.</p>
<script>if (window.opener) { window.opener.postMessage('hareno:signed-in', location.origin); window.close() } else { location.replace(%s) }</script>`, home)
	case "silent": // done, so the page does not try again if the browser refused the cookie
		http.Redirect(w, r, s.Home+mark(string(ret), "signin=done"), http.StatusFound)
	default:
		http.Redirect(w, r, s.Home+string(ret), http.StatusFound)
	}
}

func (s *Signin) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	id, sess := s.lookup(r)
	if sess != nil {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
	}
	setCookie(w, r, sessionCookie, "", -1)
	to := s.Home
	if sess != nil && s.Logout != "" {
		to = s.Logout + "?" + url.Values{"client_id": {s.OAuth.ClientID}, "id_token_hint": {sess.idToken},
			"post_logout_redirect_uri": {s.Home}}.Encode()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"redirect": to})
}

func (s *Signin) Me(w http.ResponseWriter, r *http.Request) {
	var me struct {
		Name *string   `json:"name"` // null when signed out: not an error, so the browser logs none
		Demo []Account `json:"demo,omitempty"`
	}
	me.Demo = s.Demo
	if _, sess := s.lookup(r); sess != nil {
		me.Name = &sess.name
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(me)
}

// Wrap authenticates API requests from the session cookie. A request that already carries
// Authorization (a script with its own token) passes through. Cookie-authenticated writes must
// be JSON: a cross-origin page cannot send that without a CORS preflight, which Hareno never
// grants, and SameSite=Lax does not cover other ports on the same host.
func (s *Signin) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if _, sess := s.lookup(r); sess != nil {
				if r.Method != http.MethodGet && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
					http.Error(w, `{"error":"expected application/json"}`, http.StatusUnsupportedMediaType)
					return
				}
				if tok, err := sess.src.Token(); err == nil {
					r.Header.Set("Authorization", "Bearer "+tok.AccessToken)
				} // else the refresh failed (session ended in Kiban): the handler answers 401
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Signin) lookup(r *http.Request) (string, *session) {
	c, err := r.Cookie(sessionCookie)
	id := valueOf(c, err)
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[id]
	now := time.Now()
	for k, v := range s.sessions { // ponytail: sweep on every lookup; fine for hundreds of sessions
		if now.Sub(v.used) > idle {
			delete(s.sessions, k)
		}
	}
	if sess == nil || now.Sub(sess.used) > idle {
		return "", nil
	}
	sess.used = now
	return id, sess
}

func setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
}

func valueOf(c *http.Cookie, err error) string {
	if err != nil {
		return ""
	}
	return c.Value
}

// displayName reads the name claims of an ID token that came straight from Kiban's token endpoint
// over the server's own connection. For display only: identity for decisions comes from the
// verified access token (authz.User).
func displayName(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var c struct {
		Name     string `json:"name"`
		Username string `json:"preferred_username"`
	}
	json.Unmarshal(b, &c)
	if c.Name != "" {
		return c.Name
	}
	return c.Username
}
