// S2 spike harness: Kiban v0.1.2 as Hareno's authorization, exercised through the Go SDK
// against the dev Kiban (dev/kiban, seeded by seed.sh). Throwaway spike code; the findings are
// the deliverable (FINDINGS.md).
//
//	go run . [-skip-disable] [-load 10s]
//
// Sections: login and token verification, BatchCan, bounded-scan catalog listing, company
// isolation, position reassignment, check latency under concurrency, member deactivation,
// Keycloak disablement. Every tuple the run writes is revoked at the end.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rosschiu/kiban/sdk"
)

const (
	app      = "hareno"
	feature  = "hareno.use"
	devPW    = "hareno-dev-password"
	nReports = 1000 // ALPHA catalog size
)

var (
	env     = map[string]string{}
	gw, kc  string
	httpc   = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 256}}
	kiban   *sdk.Client
	ctx     = context.Background()
	failed  bool
	written = map[string][]sdk.Tuple{} // companyID -> tuples to revoke at the end
)

func main() {
	skipDisable := flag.Bool("skip-disable", false, "skip the Keycloak disablement test (waits up to the access-token lifetime)")
	loadFor := flag.Duration("load", 10*time.Second, "duration of each load level")
	flag.Parse()

	readEnv(filepath.Join("..", "..", "dev", "kiban", ".env"))
	gw = "http://localhost:" + env["KIBAN_GATEWAY_HTTP_HOST_PORT"]
	kc = "http://localhost:" + env["KEYCLOAK_HOST_PORT"]
	secret := strings.TrimSpace(readFile(filepath.Join("..", "..", "dev", "kiban", "hareno-backend-secret")))

	var err error
	kiban, err = sdk.New(ctx, sdk.Config{GatewayURL: gw, ClientID: "hareno-backend", ClientSecret: secret, HTTPClient: httpc})
	must(err)
	defer cleanup()

	section("login and token verification")
	for _, u := range []string{"alice", "bob", "carol", "dave"} {
		t := token(u)
		sub, err := kiban.VerifyUserToken(ctx, t)
		check(u+": VerifyUserToken returns the Keycloak subject", err == nil && sub == kcUserID(u), fmt.Sprint(sub, err))
	}
	cs, err := kiban.MeCompanies(ctx, token("dave"))
	must(err)
	codes := []string{}
	for _, c := range cs {
		codes = append(codes, c.Code)
	}
	slices.Sort(codes)
	check("dave: MeCompanies = ALPHA,BETA", strings.Join(codes, ",") == "ALPHA,BETA", fmt.Sprint(codes))
	_, err = kiban.VerifyUserToken(ctx, token("alice")[:40]+"x"+token("alice")[41:])
	check("tampered token rejected", err != nil, "")

	alpha, beta := companyID("alice", "ALPHA"), companyID("carol", "BETA")
	sa := token(env["KIBAN_SUPERADMIN_USERNAME"])
	northMgr := ensureHolder(sa, alpha, "NORTH_MGR", "alice") // a crashed run may have left it reassigned
	finLead := ensureHolder(sa, beta, "FIN_LEAD", "carol")
	finance := group(alpha, "FINANCE")

	// The catalog: Hareno's own rows (here a slice), each anchored to its company in Kiban.
	run := time.Now().Format("150405")
	var catalog []string
	var tuples []sdk.Tuple
	for i := range nReports {
		id := fmt.Sprintf("s2-%s-a%04d", run, i)
		catalog = append(catalog, id)
		tuples = append(tuples, sdk.AnchorTuple(app, "report", id, alpha))
		switch {
		case i%100 == 0:
			tuples = append(tuples, viewer(id, "position", northMgr.ID, "holder"))
		case i%10 == 1:
			tuples = append(tuples, viewer(id, "group", finance, "member"))
		case i == 2:
			tuples = append(tuples, viewer(id, "user", kcUserID("bob"), ""))
		}
	}
	betaReport := fmt.Sprintf("s2-%s-b0000", run)
	section("grants")
	t0 := time.Now()
	grant(alpha, tuples...)
	fmt.Printf("     %d tuples for %d ALPHA reports written in %v (batches of 100)\n", len(tuples), nReports, time.Since(t0).Round(time.Millisecond))
	grant(beta, sdk.AnchorTuple(app, "report", betaReport, beta), viewer(betaReport, "position", finLead.ID, "holder"))

	section("BatchCan")
	want := map[string][]int{"alice": {0}, "bob": {1, 2, 11, 21, 31, 41, 51, 61, 71, 81, 91}, "carol": {}, "dave": {1, 11, 21, 31, 41, 51, 61, 71, 81, 91}}
	for _, u := range []string{"alice", "bob", "carol", "dave"} {
		got, err := visible(u, alpha, catalog[:100])
		check(fmt.Sprintf("%s: visible among ALPHA reports 0-99 = %v", u, want[u]), err == nil && slices.Equal(got, want[u]), fmt.Sprint(got, err))
	}
	d := can("carol", beta, betaReport)
	check("carol: BETA report via FIN_LEAD position", d.Allowed, d.Reason)

	section("bounded-scan catalog listing (page 25, batch 100, scan cap 500)")
	for _, u := range []string{"alice", "bob"} {
		cursor, total := 0, 0
		for page := 1; cursor >= 0; page++ {
			t := time.Now()
			items, next, trips := list(u, alpha, catalog, cursor, 25, 100, 500)
			fmt.Printf("     %-5s page %d: %2d items, scanned %4d-%4d, %d BatchCan calls, %v\n",
				u, page, len(items), cursor, ifElse(next < 0, len(catalog), next), trips, time.Since(t).Round(time.Millisecond))
			total += len(items)
			cursor = next
		}
		check(u+": listing returns every visible report exactly once", total == map[string]int{"alice": 10, "bob": 101}[u], fmt.Sprint(total))
	}

	section("company isolation")
	d = can("carol", alpha, catalog[0])
	check("carol (BETA only) asks about an ALPHA report in ALPHA: denied", !d.Allowed, d.Reason)
	d = can("dave", alpha, catalog[1])
	check("dave asks about an ALPHA report he may view, in ALPHA: allowed (control)", d.Allowed, d.Reason)
	d = can("dave", beta, catalog[1])
	check("dave asks about the same ALPHA report in BETA context: denied", !d.Allowed, d.Reason)
	err = kiban.Grant(ctx, beta, viewer(catalog[5], "user", kcUserID("carol"), ""))
	note(beta, err, viewer(catalog[5], "user", kcUserID("carol"), ""))
	d = can("carol", beta, catalog[5])
	check("tuple on an ALPHA report written under BETA: carol still denied in BETA", !d.Allowed, fmt.Sprintf("grant err=%v; %s", err, d.Reason))
	err = kiban.Grant(ctx, alpha, viewer(catalog[6], "position", finLead.ID, "holder"))
	note(alpha, err, viewer(catalog[6], "position", finLead.ID, "holder"))
	check("ALPHA report granted to a BETA position: grant rejected", err != nil, "grant accepted")
	// One holder per position (409 otherwise): hand FIN_LEAD from carol to dave, then back.
	ensureHolder(sa, beta, "FIN_LEAD", "dave")
	d = can("dave", alpha, catalog[6])
	check("dave holds BETA's FIN_LEAD: ALPHA report granted to that position stays denied in ALPHA", !d.Allowed, "allowed: "+d.Reason)
	finLead = ensureHolder(sa, beta, "FIN_LEAD", "carol")
	err = kiban.Grant(ctx, beta, sdk.AnchorTuple(app, "report", catalog[11], beta))
	note(beta, err, sdk.AnchorTuple(app, "report", catalog[11], beta))
	d = can("dave", beta, catalog[11])
	check("second anchor binding an ALPHA report to BETA: rejected, or dave still denied in BETA", err != nil || !d.Allowed,
		fmt.Sprintf("grant err=%v; dave in BETA: %v %s", err, d.Allowed, d.Reason))

	section("position reassignment (NORTH_MGR: alice -> bob -> alice)")
	alpha0 := catalog[0] // NORTH_MGR only
	err = adminErr(sa, "POST", "/api/org/admin/positions/"+northMgr.ID+"/assignments", map[string]string{"memberId": member(alpha, "bob")}, nil)
	check("assigning bob while alice still holds NORTH_MGR is refused (409)", err != nil && strings.Contains(err.Error(), "409"), fmt.Sprint(err))
	t0 = time.Now()
	admin(sa, "POST", "/api/org/admin/assignments/"+northMgr.AssignmentID+"/end", nil, nil)
	gap := time.Now()
	da, db := can("alice", alpha, alpha0), can("bob", alpha, alpha0)
	check("between end and assign nobody holds NORTH_MGR: alice and bob both denied", !da.Allowed && !db.Allowed,
		fmt.Sprintf("alice %v, bob %v", da.Allowed, db.Allowed))
	var asg struct{ ID string }
	admin(sa, "POST", "/api/org/admin/positions/"+northMgr.ID+"/assignments", map[string]string{"memberId": member(alpha, "bob")}, &asg)
	fmt.Printf("     no-holder window (end returned -> assign returned, including the two checks): %v\n", time.Since(gap).Round(time.Millisecond))
	writeDone := time.Since(t0)
	da, db = can("alice", alpha, alpha0), can("bob", alpha, alpha0)
	check(fmt.Sprintf("first check after reassignment (writes took %v): alice denied, bob allowed", writeDone.Round(time.Millisecond)), !da.Allowed && db.Allowed,
		fmt.Sprintf("alice %v %s, bob %v %s", da.Allowed, da.Reason, db.Allowed, db.Reason))
	admin(sa, "POST", "/api/org/admin/assignments/"+asg.ID+"/end", nil, nil)
	err = adminErr(sa, "POST", "/api/org/admin/positions/"+northMgr.ID+"/assignments", map[string]string{"memberId": member(alpha, "alice")}, nil)
	check("same-day reassignment back to alice", err == nil, fmt.Sprint(err))
	da = can("alice", alpha, alpha0)
	check("alice allowed again", da.Allowed, da.Reason)

	section("revoke order (anchor first)")
	obj := fmt.Sprintf("s2-%s-orphan", run)
	must(kiban.Grant(ctx, alpha, sdk.AnchorTuple(app, "report", obj, alpha), viewer(obj, "user", kcUserID("alice"), "")))
	err = kiban.Revoke(ctx, alpha, sdk.AnchorTuple(app, "report", obj, alpha))
	check("revoking the anchor while a viewer tuple remains is accepted", err == nil, fmt.Sprint(err))
	d = can("alice", alpha, obj)
	check("alice denied once the anchor is gone", !d.Allowed, d.Reason)
	err = kiban.Revoke(ctx, alpha, viewer(obj, "user", kcUserID("alice"), ""))
	check("revoking the remaining viewer tuple is refused (422 not anchored)", err != nil, "revoke accepted")
	fmt.Println("     refusal:", err)
	must(kiban.Grant(ctx, alpha, sdk.AnchorTuple(app, "report", obj, alpha)))
	d = can("alice", alpha, obj)
	check("re-anchoring the same id silently revives alice's old viewer grant", d.Allowed, d.Reason)
	must(kiban.Revoke(ctx, alpha, viewer(obj, "user", kcUserID("alice"), "")))
	must(kiban.Revoke(ctx, alpha, sdk.AnchorTuple(app, "report", obj, alpha)))

	section("check latency under concurrency")
	loadTest(alpha, catalog, *loadFor)

	section("member deactivation in Kiban (bob)")
	bobMember := member(alpha, "bob")
	bobToken := token("bob")
	setMemberActive(sa, bobMember, "bob", false)
	t0 = time.Now()
	d = can("bob", alpha, catalog[1])
	check(fmt.Sprintf("bob's existing token, first check after deactivation (%v): denied", time.Since(t0).Round(time.Millisecond)), !d.Allowed, d.Reason)
	cs, err = kiban.MeCompanies(ctx, bobToken)
	check("bob's company list no longer shows ALPHA", err == nil && len(cs) == 0, fmt.Sprint(cs, err))
	setMemberActive(sa, bobMember, "bob", true)
	d = can("bob", alpha, catalog[1])
	check("bob reactivated: allowed again", d.Allowed, d.Reason)

	if *skipDisable {
		fmt.Println("\n(skipped Keycloak disablement test)")
		return
	}
	section("Keycloak disablement (dave)")
	disablement(alpha, catalog[1])
}

// ---- Kiban helpers ----------------------------------------------------------------------------

func viewer(id, subjectType, subjectID, subjectRelation string) sdk.Tuple {
	return sdk.Tuple{ObjectType: "report", ObjectID: id, Relation: "viewer", SubjectType: subjectType, SubjectID: subjectID, SubjectRelation: subjectRelation}
}

func grant(company string, tuples ...sdk.Tuple) {
	for chunk := range slices.Chunk(tuples, 100) {
		must(kiban.Grant(ctx, company, chunk...))
		written[company] = append(written[company], chunk...)
	}
}

// note records a tuple the run tried to write, so cleanup revokes it if Kiban accepted it.
func note(company string, err error, t sdk.Tuple) {
	if err == nil {
		written[company] = append(written[company], t)
	}
}

func cleanup() {
	if r := recover(); r != nil {
		fmt.Println("PANIC:", r)
		failed = true
	}
	for company, tuples := range written {
		// Relations before anchors: Kiban refuses to revoke a relation on an object whose anchor is gone.
		slices.SortStableFunc(tuples, func(a, b sdk.Tuple) int { return strings.Compare(b.Relation, a.Relation) })
		for chunk := range slices.Chunk(tuples, 100) {
			if err := kiban.Revoke(ctx, company, chunk...); err != nil {
				fmt.Println("cleanup:", err)
			}
		}
	}
	if failed {
		fmt.Println("\nSOME CHECKS FAILED")
		os.Exit(1)
	}
}

func checkReq(company, id string) sdk.CanRequest {
	r := sdk.CanRequest{FeatureKey: feature, ModuleKey: app, CompanyID: company, Relation: "viewer"}
	r.Object = &struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}{"report", id}
	return r
}

func can(user, company, id string) sdk.Decision {
	d, err := kiban.Can(ctx, token(user), checkReq(company, id))
	if err != nil {
		return sdk.Decision{Reason: "error: " + err.Error()}
	}
	return d
}

// visible answers which of ids the user may view, as catalog indices (ids end in a 4-digit index).
func visible(user, company string, ids []string) ([]int, error) {
	items := make([]sdk.BatchItem, len(ids))
	for i, id := range ids {
		items[i] = sdk.NewBatchItem("report", id, "viewer")
	}
	req := sdk.CanRequest{FeatureKey: feature, ModuleKey: app, CompanyID: company}
	res, err := kiban.BatchCan(ctx, token(user), req, items)
	if err != nil {
		return nil, err
	}
	out := []int{}
	for _, r := range res {
		if r.Decision.Allowed {
			var n int
			fmt.Sscanf(r.Object.ID[len(r.Object.ID)-4:], "%d", &n)
			out = append(out, n)
		}
	}
	return out, nil
}

// list is the bounded-scan listing Hareno's catalog would run (§10.4 item 2): walk candidate
// rows in a stable order from cursor, batch-check them, stop when the page is full, the
// candidates run out, or maxScan rows were checked. Returns the page, the next cursor (-1 when
// exhausted) and the number of BatchCan round trips. No total count is ever returned.
func list(user, company string, rows []string, cursor, pageSize, batch, maxScan int) ([]int, int, int) {
	var page []int
	trips, scanned := 0, 0
	for cursor < len(rows) && len(page) < pageSize && scanned < maxScan {
		end := min(cursor+batch, len(rows))
		got, err := visible(user, company, rows[cursor:end])
		must(err)
		trips++
		for _, n := range got {
			if len(page) == pageSize {
				end = n // resume at the first visible row that did not fit
				break
			}
			page = append(page, n)
		}
		scanned += end - cursor
		cursor = end
	}
	if cursor >= len(rows) {
		cursor = -1
	}
	return page, cursor, trips
}

// ---- load -------------------------------------------------------------------------------------

func loadTest(company string, catalog []string, dur time.Duration) {
	users := []string{"alice", "bob", "dave"}
	for _, u := range users {
		token(u) // fresh tokens before timing
	}
	fmt.Println("     kind       workers   checks/s   p50      p95      p99      errors")
	run := func(kind string, workers int, op func(w, i int) error) {
		var mu sync.Mutex
		var lat []time.Duration
		errs := 0
		stop := time.Now().Add(dur)
		var wg sync.WaitGroup
		for w := range workers {
			wg.Go(func() {
				var mine []time.Duration
				bad := 0
				for i := 0; time.Now().Before(stop); i++ {
					t := time.Now()
					if op(w, i) != nil {
						bad++
					}
					mine = append(mine, time.Since(t))
				}
				mu.Lock()
				lat, errs = append(lat, mine...), errs+bad
				mu.Unlock()
			})
		}
		wg.Wait()
		slices.Sort(lat)
		pct := func(p float64) string { return lat[int(p*float64(len(lat)-1))].Round(100 * time.Microsecond).String() }
		perCall := 1
		if kind == "BatchCan" {
			perCall = 100
		}
		fmt.Printf("     %-10s %7d %10.0f   %-8s %-8s %-8s %d\n", kind, workers, float64(len(lat)*perCall)/dur.Seconds(), pct(.5), pct(.95), pct(.99), errs)
	}
	toks := map[string]string{}
	for _, u := range users {
		toks[u] = token(u)
	}
	for _, workers := range []int{1, 8, 32, 64} {
		run("Can", workers, func(w, i int) error {
			_, err := kiban.Can(ctx, toks[users[w%3]], checkReq(company, catalog[(w*37+i)%len(catalog)]))
			return err
		})
	}
	items := make([]sdk.BatchItem, 100)
	for i := range items {
		items[i] = sdk.NewBatchItem("report", catalog[i], "viewer")
	}
	for _, workers := range []int{1, 8, 32} {
		run("BatchCan", workers, func(w, i int) error {
			_, err := kiban.BatchCan(ctx, toks[users[w%3]], sdk.CanRequest{FeatureKey: feature, ModuleKey: app, CompanyID: company}, items)
			return err
		})
	}
}

// ---- disablement ------------------------------------------------------------------------------

func disablement(company, report string) {
	access, refresh, exp := login("dave", devPW)
	d, _ := kiban.Can(ctx, access, checkReq(company, report))
	check("dave allowed before disablement", d.Allowed, d.Reason)
	kcAdmin("PUT", "/users/"+kcUserID("dave"), map[string]any{"enabled": false})
	defer kcAdmin("PUT", "/users/"+kcUserID("dave"), map[string]any{"enabled": true})
	disabledAt := time.Now()
	fmt.Printf("     dave disabled in Keycloak; access token expires in %v\n", time.Until(exp).Round(time.Second))

	_, err := refreshToken(refresh)
	check("refresh token refused immediately after disablement", err != nil, "refresh succeeded")
	_, _, err = loginErr("dave", devPW)
	check("new login refused", err != nil, "login succeeded")
	cs, err := kiban.MeCompanies(ctx, access)
	fmt.Printf("     MeCompanies with the disabled user's unexpired token: %d companies, err %v\n", len(cs), err)
	_, verr := kiban.VerifyUserToken(ctx, access)
	fmt.Printf("     VerifyUserToken with that token: err %v\n", verr)

	for {
		d, err := kiban.Can(ctx, access, checkReq(company, report))
		_, verr := kiban.VerifyUserToken(ctx, access)
		if err != nil || !d.Allowed || verr != nil {
			lost := time.Since(disabledAt).Round(time.Second)
			reason := d.Reason
			if err != nil {
				reason = err.Error()
			}
			fmt.Printf("     access lost %v after disablement (token expiry was %v after); Can: %s; VerifyUserToken err: %v\n",
				lost, exp.Sub(disabledAt).Round(time.Second), reason, verr)
			check("access lost no later than token expiry + 5 s", time.Now().Before(exp.Add(5*time.Second)), "")
			return
		}
		if time.Now().After(exp.Add(30 * time.Second)) {
			check("access lost", false, "still allowed 30 s after token expiry")
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// ---- Kiban and Keycloak admin ----------------------------------------------------------------

type positionRow struct {
	ID           string `json:"id"`
	Code         string `json:"code"`
	AssignmentID string `json:"assignmentId"`
	MemberID     string `json:"memberId"`
}

func position(company, code string) positionRow {
	var out struct{ Items []positionRow }
	admin(token(env["KIBAN_SUPERADMIN_USERNAME"]), "GET", "/api/org/admin/companies/"+company+"/positions", nil, &out)
	for _, p := range out.Items {
		if p.Code == code {
			return p
		}
	}
	panic("no position " + code)
}

// ensureHolder makes user the holder of the position (ending any other holder's assignment).
func ensureHolder(sa, company, code, user string) positionRow {
	p := position(company, code)
	want := member(company, user)
	if p.MemberID == want {
		return p
	}
	if p.AssignmentID != "" {
		admin(sa, "POST", "/api/org/admin/assignments/"+p.AssignmentID+"/end", nil, nil)
	}
	admin(sa, "POST", "/api/org/admin/positions/"+p.ID+"/assignments", map[string]string{"memberId": want}, nil)
	return position(company, code)
}

func group(company, code string) string {
	var out struct{ Items []struct{ ID, Code string } }
	admin(token(env["KIBAN_SUPERADMIN_USERNAME"]), "GET", "/api/org/admin/companies/"+company+"/groups", nil, &out)
	for _, g := range out.Items {
		if g.Code == code {
			return g.ID
		}
	}
	panic("no group " + code)
}

func member(company, code string) string {
	var out struct{ Items []struct{ ID, Code string } }
	admin(token(env["KIBAN_SUPERADMIN_USERNAME"]), "GET", "/api/org/admin/members?companyId="+company+"&pageSize=100", nil, &out)
	for _, m := range out.Items {
		if strings.EqualFold(m.Code, code) { // Kiban upper-cases codes
			return m.ID
		}
	}
	panic(fmt.Sprintf("no member %s in %s: %+v", code, company, out.Items))
}

func setMemberActive(sa, id, code string, active bool) {
	admin(sa, "PUT", "/api/org/admin/members/"+id, map[string]any{"displayName": code, "email": code + "@example.invalid", "isActive": active}, nil)
}

func companyID(user, code string) string {
	cs, err := kiban.MeCompanies(ctx, token(user))
	must(err)
	for _, c := range cs {
		if c.Code == code {
			return c.ID
		}
	}
	panic("no company " + code)
}

func admin(bearer, method, path string, in, out any) { must(adminErr(bearer, method, path, in, out)) }

func adminErr(bearer, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, gw+path, body)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, raw)
	}
	if out != nil {
		var env struct{ Data json.RawMessage }
		must(json.Unmarshal(raw, &env))
		must(json.Unmarshal(env.Data, out))
	}
	return nil
}

var kcIDs = map[string]string{}

func kcUserID(user string) string {
	if id, ok := kcIDs[user]; ok {
		return id
	}
	var out []struct{ ID string }
	kcAdminOut("GET", "/users?exact=true&username="+url.QueryEscape(user), nil, &out)
	kcIDs[user] = out[0].ID
	return out[0].ID
}

func kcAdmin(method, path string, in any) { kcAdminOut(method, path, in, nil) }

func kcAdminOut(method, path string, in, out any) {
	form := url.Values{"grant_type": {"password"}, "client_id": {"admin-cli"},
		"username": {env["KC_BOOTSTRAP_ADMIN_USERNAME"]}, "password": {env["KC_BOOTSTRAP_ADMIN_PASSWORD"]}}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	postForm(kc+"/realms/master/protocol/openid-connect/token", form, &tok)
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, kc+"/admin/realms/"+realm()+path, body)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpc.Do(req)
	must(err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		panic(fmt.Sprintf("keycloak %s %s: HTTP %d: %s", method, path, resp.StatusCode, raw))
	}
	if out != nil {
		must(json.Unmarshal(raw, out))
	}
}

// ---- login ------------------------------------------------------------------------------------

type tok struct {
	access string
	exp    time.Time
}

var tokens = map[string]tok{}

// token returns a cached access token for user, logging in again when it has under a minute left.
func token(user string) string {
	if t, ok := tokens[user]; ok && time.Until(t.exp) > time.Minute {
		return t.access
	}
	pw := devPW
	if user == env["KIBAN_SUPERADMIN_USERNAME"] {
		pw = strings.TrimSpace(readFile(filepath.Join("..", "..", "dev", "kiban", "secrets-in", "superadmin-password")))
	}
	access, _, exp := login(user, pw)
	tokens[user] = tok{access, exp}
	return access
}

func login(user, pw string) (string, string, time.Time) {
	a, r, err := loginErr(user, pw)
	must(err)
	return a, r, time.Now().Add(time.Duration(expiresIn(a)) * time.Second)
}

var formAction = regexp.MustCompile(`id="kc-form-login"[^>]*action="([^"]*)"`)

// loginErr runs the browser's PKCE authorization-code login through the gateway, as the
// kiban-frontend public client; returns the access and refresh tokens.
func loginErr(user, pw string) (string, string, error) {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	redirect := "http://localhost:5173/callback"
	vb := make([]byte, 48)
	rand.Read(vb)
	verifier := base64.RawURLEncoding.EncodeToString(vb)
	sum := sha256.Sum256([]byte(verifier))
	authURL := gw + "/auth/realms/" + realm() + "/protocol/openid-connect/auth?" + url.Values{
		"client_id": {"kiban-frontend"}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {"openid"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "state": {"s2"},
	}.Encode()
	resp, err := c.Get(authURL)
	if err != nil {
		return "", "", err
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := formAction.FindSubmatch(page)
	if m == nil {
		return "", "", errors.New("no login form")
	}
	resp, err = c.PostForm(html.UnescapeString(string(m[1])), url.Values{"username": {user}, "password": {pw}, "credentialId": {""}})
	if err != nil {
		return "", "", err
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := ""
	if loc != nil {
		code = loc.Query().Get("code")
	}
	if code == "" {
		return "", "", fmt.Errorf("login refused for %s (HTTP %d)", user, resp.StatusCode)
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	err = postFormErr(gw+"/auth/realms/"+realm()+"/protocol/openid-connect/token", url.Values{"grant_type": {"authorization_code"},
		"code": {code}, "redirect_uri": {redirect}, "client_id": {"kiban-frontend"}, "code_verifier": {verifier}}, &out)
	return out.AccessToken, out.RefreshToken, err
}

func refreshToken(refresh string) (string, error) {
	var out struct {
		AccessToken string `json:"access_token"`
	}
	err := postFormErr(gw+"/auth/realms/"+realm()+"/protocol/openid-connect/token",
		url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {"kiban-frontend"}}, &out)
	return out.AccessToken, err
}

func expiresIn(jwt string) int64 {
	parts := strings.Split(jwt, ".")
	b, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct{ Exp, Iat int64 }
	json.Unmarshal(b, &c)
	return c.Exp - c.Iat
}

func postForm(u string, form url.Values, out any) { must(postFormErr(u, form, out)) }

func postFormErr(u string, form url.Values, out any) error {
	resp, err := httpc.PostForm(u, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}

// ---- small things -----------------------------------------------------------------------------

func realm() string {
	if r := env["KEYCLOAK_REALM"]; r != "" {
		return r
	}
	return "kiban"
}

func readEnv(path string) {
	f, err := os.Open(path)
	must(err)
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if k, v, ok := strings.Cut(s.Text(), "="); ok && !strings.HasPrefix(k, "#") {
			env[k] = v
		}
	}
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	must(err)
	return string(b)
}

func section(name string) { fmt.Printf("\n== %s\n", name) }

func check(name string, ok bool, detail string) {
	if ok {
		fmt.Println("ok   " + name)
		return
	}
	failed = true
	fmt.Printf("FAIL %s: %s\n", name, detail)
}

func ifElse[T any](c bool, a, b T) T {
	if c {
		return a
	}
	return b
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
