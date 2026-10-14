package sqlapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rosschiu/kiban/sdk"
)

// Sharing a report with people (R1-4b). The report's editors manage it:
//
//	GET    /api/reports/{id}/people?q=     company members to share with (Kiban's member directory)
//	GET    /api/reports/{id}/shares        who it is shared with
//	POST   /api/reports/{id}/shares        {"member", "name", "relation": "viewer"|"editor"}
//	DELETE /api/reports/{id}/shares/{member}
//
// A share is the Kiban tuple report:<id>#<relation> @ member:<member>#mapped_user: it follows the
// member's linked login and ends with their membership. Positions and groups follow when Kiban
// 0.2 lets members list them (ARCHITECTURE-PLAN §10.4).

// People searches a company's member directory as the user; authz.Directory satisfies it.
type People interface {
	Search(ctx context.Context, bearer, companyID, q string) ([]sdk.DirectoryEntry, error)
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
		SharedAt time.Time `json:"sharedAt"`
	}
	rows, _ := h.DB.Query(r.Context(), `SELECT member, name, relation, shared_at FROM report_share WHERE report_id = $1 ORDER BY name`, rep.ID)
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
	}
	if !decode(w, r, &req) || req.Member == "" || req.Name == "" || (req.Relation != "viewer" && req.Relation != "editor") {
		fail(w, http.StatusBadRequest, `expected {"member": ..., "name": ..., "relation": "viewer" or "editor"}`)
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
		if _, err := tx.Exec(ctx, `INSERT INTO report_share (report_id, member, name, relation, shared_by) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (report_id, member) DO UPDATE SET name = $3, relation = $4, shared_by = $5, shared_at = now()`,
			rep.ID, who.ID, who.Name, req.Relation, sub); err != nil {
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
	h.Log.Info("report shared", "user", sub, "report", rep.ID, "member", who.ID, "relation", req.Relation)
	reply(w, map[string]any{"member": who.ID, "name": who.Name, "relation": req.Relation})
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
