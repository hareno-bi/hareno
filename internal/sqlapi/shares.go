package sqlapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rosschiu/kiban/sdk"

	"github.com/hareno-bi/hareno/internal/authz"
	"github.com/hareno-bi/hareno/internal/sqlexec"
)

// Sharing a report with people (R1-4b). The report's editors manage it:
//
//	GET    /api/reports/{id}/people?q=     company members to share with (Kiban's member directory)
//	GET    /api/reports/{id}/shares        who it is shared with
//	POST   /api/reports/{id}/shares        {"member", "name", "relation": "viewer"|"editor", "data": "viewer"|"sharer"}
//	DELETE /api/reports/{id}/shares/{member}
//
// A share is the Kiban tuple report:<id>#<relation> @ member:<member>#mapped_user: it follows the
// member's linked login and ends with their membership. Positions and groups follow when Kiban
// 0.2 lets members list them (ARCHITECTURE-PLAN §10.4).
//
// Whose rows a viewer sees ("data"): their own by default, so a share gives no data the viewer
// could not already query. A connection's data_sharer may instead share a report showing their
// own rows, to viewers only. Kiban cannot answer for the sharer without the sharer's token, so
// Hareno keeps the sharer's row scope with the share and refreshes it whenever the sharer opens
// the report; it stops applying when the sharer leaves the company.
// ponytail: a sharer whose scope shrinks keeps showing the old scope until they next open the
// report; needs a Kiban check on behalf of another subject (ARCHITECTURE-PLAN §10.4 item 17).

// People searches a company's member directory as the user; authz.Directory satisfies it.
type People interface {
	Search(ctx context.Context, bearer, companyID, q string) ([]sdk.DirectoryEntry, error)
}

// Members says which member of a company a login is, as Hareno's service account; *sdk.Client
// satisfies it.
type Members interface {
	MemberBySubject(ctx context.Context, companyID, subject string) (sdk.MemberFact, error)
}

// member is the active member of the company that sub signs in as, or "".
func (h *Handler) member(ctx context.Context, company, sub string) (string, error) {
	f, err := h.Members.MemberBySubject(ctx, company, sub)
	if err != nil || !f.IsMember || !f.IsActive || f.MemberID == nil {
		return "", err
	}
	return *f.MemberID, nil
}

// sharedScope is the sharer's row scope when the report was shared with the user showing the
// sharer's data, and the sharer is still in the company.
func (h *Handler) sharedScope(ctx context.Context, sub string, rep reportRow, rt rights) (sqlexec.Scope, bool, error) {
	me, err := h.member(ctx, rt.company, sub)
	if err != nil || me == "" {
		return nil, false, err
	}
	var by string
	var scope sqlexec.Scope
	err = h.DB.QueryRow(ctx, `SELECT shared_by, scope FROM report_share WHERE report_id = $1 AND member = $2 AND data = 'sharer'`,
		rep.ID, me).Scan(&by, &scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	sharer, err := h.member(ctx, rt.company, by)
	if err != nil || sharer == "" {
		return nil, false, err // the sharer's data left with them
	}
	return scope, true, nil
}

// refresh brings the shares the editor gave with their data to their current row scope, or, if
// they may no longer share their data, back to each viewer's own. It says whether they may.
// The permission is per data source; a report uses one. When a report or query spans several,
// the strictest applies: data_sharer on every one of them, or each viewer sees their own.
func (h *Handler) refresh(ctx context.Context, bearer, sub string, rep reportRow, rt rights) (bool, error) {
	may := authz.Item{Type: "connection", ID: rep.Connection, Relation: "data_sharer"}
	ok, err := h.Authz.Allowed(ctx, bearer, sub, rt.company, []authz.Item{may})
	if err != nil {
		return false, err
	}
	if !ok[may] {
		_, err = h.DB.Exec(ctx, `UPDATE report_share SET data = 'viewer', scope = NULL WHERE report_id = $1 AND shared_by = $2 AND data = 'sharer'`, rep.ID, sub)
		return false, err
	}
	scope, err := h.ownScope(ctx, bearer, sub, rep)
	if err == nil {
		_, err = h.DB.Exec(ctx, `UPDATE report_share SET scope = $3 WHERE report_id = $1 AND shared_by = $2 AND data = 'sharer' AND scope IS DISTINCT FROM $3::jsonb`,
			rep.ID, sub, scope)
	}
	return err == nil, err
}

func shareTuple(report, member, relation string) sdk.Tuple {
	return sdk.Tuple{ObjectType: "report", ObjectID: report, Relation: relation, SubjectType: "member", SubjectID: member, SubjectRelation: "mapped_user"}
}

// managed loads a report for a sharing change: editors only.
func (h *Handler) managed(w http.ResponseWriter, r *http.Request) (reportRow, rights, string, string, bool) {
	rep, rt, bearer, sub, ok := h.load(w, r)
	if ok && !rt.edit {
		fail(w, http.StatusForbidden, "only the report's editors may share it")
		ok = false
	}
	return rep, rt, bearer, sub, ok
}

type person struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// people are the directory's matches that can hold a share: people with a login.
func (h *Handler) people(ctx context.Context, bearer, company, q string) ([]person, error) {
	entries, err := h.People.Search(ctx, bearer, company, q)
	if err != nil {
		return nil, err
	}
	out := []person{}
	for _, e := range entries {
		if e.Kind == "person" && e.HasLinkedUser {
			out = append(out, person{e.ID, e.DisplayName, e.Email})
		}
	}
	return out, nil
}

func (h *Handler) SearchPeople(w http.ResponseWriter, r *http.Request) {
	_, rt, bearer, _, ok := h.managed(w, r)
	if !ok {
		return
	}
	found, err := h.people(r.Context(), bearer, rt.company, r.URL.Query().Get("q"))
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "the member directory is unavailable")
		return
	}
	reply(w, map[string]any{"people": found})
}

func (h *Handler) ListShares(w http.ResponseWriter, r *http.Request) {
	rep, _, _, _, ok := h.managed(w, r)
	if !ok {
		return
	}
	type share struct {
		Member   string    `json:"member"`
		Name     string    `json:"name"`
		Relation string    `json:"relation"`
		Data     string    `json:"data"`
		SharedAt time.Time `json:"sharedAt"`
	}
	rows, _ := h.DB.Query(r.Context(), `SELECT member, name, relation, data, shared_at FROM report_share WHERE report_id = $1 ORDER BY name`, rep.ID)
	shares, err := pgx.CollectRows(rows, pgx.RowToStructByPos[share])
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not read the shares")
		return
	}
	reply(w, map[string]any{"shares": shares})
}

func (h *Handler) Share(w http.ResponseWriter, r *http.Request) {
	rep, rt, bearer, sub, ok := h.managed(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var req struct {
		Member   string `json:"member"`
		Name     string `json:"name"` // how the picker found them; the stored name comes from Kiban
		Relation string `json:"relation"`
		Data     string `json:"data"` // whose rows: "viewer" (theirs; the default) or "sharer"
	}
	if !decode(w, r, &req) || req.Member == "" || req.Name == "" || (req.Relation != "viewer" && req.Relation != "editor") {
		fail(w, http.StatusBadRequest, `expected {"member": ..., "name": ..., "relation": "viewer" or "editor", "data": "viewer" or "sharer"}`)
		return
	}
	var scope sqlexec.Scope // the sharer's, for a share of their data
	switch req.Data {
	case "", "viewer":
		req.Data = "viewer"
	case "sharer":
		if req.Relation != "viewer" {
			fail(w, http.StatusBadRequest, "your data can be shared with viewers only: editors work with their own")
			return
		}
		may, err := h.refresh(ctx, bearer, sub, rep, rt)
		if err == nil && may {
			scope, err = h.ownScope(ctx, bearer, sub, rep)
		}
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "authorization unavailable")
			return
		} else if !may {
			fail(w, http.StatusForbidden, "you may not share your data from this connection")
			return
		}
	default:
		fail(w, http.StatusBadRequest, `"data" is "viewer" or "sharer"`)
		return
	}
	// The subject must be a person with a login in the report's company, as the directory shows
	// them to the sharer: an id from anywhere else is refused.
	found, err := h.people(ctx, bearer, rt.company, req.Name)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "the member directory is unavailable")
		return
	}
	var who *person
	for i := range found {
		if found[i].ID == req.Member {
			who = &found[i]
		}
	}
	if who == nil {
		fail(w, http.StatusBadRequest, "not a member of this company who can sign in")
		return
	}

	err = pgx.BeginFunc(ctx, h.DB, func(tx pgx.Tx) error {
		var old string
		err := tx.QueryRow(ctx, `SELECT relation FROM report_share WHERE report_id = $1 AND member = $2 FOR UPDATE`, rep.ID, who.ID).Scan(&old)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO report_share (report_id, member, name, relation, shared_by, data, scope) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (report_id, member) DO UPDATE SET name = $3, relation = $4, shared_by = $5, data = $6, scope = $7, shared_at = now()`,
			rep.ID, who.ID, who.Name, req.Relation, sub, req.Data, scope); err != nil {
			return err
		}
		if err := h.Grants.Grant(ctx, rt.company, shareTuple(rep.ID, who.ID, req.Relation)); err != nil {
			return err
		}
		if old != "" && old != req.Relation {
			return h.Grants.Revoke(ctx, rt.company, shareTuple(rep.ID, who.ID, old))
		}
		return nil
	})
	if err != nil {
		h.Log.Error("report share failed", "user", sub, "report", rep.ID, "member", who.ID, "error", err.Error())
		fail(w, http.StatusBadGateway, "could not share the report")
		return
	}
	h.Log.Info("report shared", "user", sub, "report", rep.ID, "member", who.ID, "relation", req.Relation, "data", req.Data, "scope", scope)
	reply(w, map[string]any{"member": who.ID, "name": who.Name, "relation": req.Relation, "data": req.Data})
}

func (h *Handler) Unshare(w http.ResponseWriter, r *http.Request) {
	rep, rt, _, sub, ok := h.managed(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	member := r.PathValue("member")
	err := pgx.BeginFunc(ctx, h.DB, func(tx pgx.Tx) error {
		var relation string
		if err := tx.QueryRow(ctx, `DELETE FROM report_share WHERE report_id = $1 AND member = $2 RETURNING relation`, rep.ID, member).Scan(&relation); err != nil {
			return err
		}
		return h.Grants.Revoke(ctx, rt.company, shareTuple(rep.ID, member, relation))
	})
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, http.StatusNotFound, "not shared with that member")
		return
	} else if err != nil {
		h.Log.Error("report unshare failed", "user", sub, "report", rep.ID, "member", member, "error", err.Error())
		fail(w, http.StatusBadGateway, "could not remove the share")
		return
	}
	h.Log.Info("report unshared", "user", sub, "report", rep.ID, "member", member)
	w.WriteHeader(http.StatusNoContent)
}
