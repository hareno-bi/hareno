package authz

import (
	"context"
	"errors"
	"sync"

	"github.com/rosschiu/kiban/sdk"
)

// Fake is an in-memory Kiban for tests. Tokens are user subjects; Grants holds
// "company|type|id|relation|subject" entries; Companies maps subject -> companies.
type Fake struct {
	mu        sync.Mutex
	Grants    map[string]bool
	Companies map[string][]sdk.Company
	Err       error // returned by every call when set
	Calls     int   // BatchCan calls
}

func (f *Fake) VerifyUserToken(_ context.Context, bearer string) (string, error) {
	if bearer == "" || bearer == "bad" {
		return "", errors.New("invalid token")
	}
	return bearer, f.Err
}

func (f *Fake) BatchCan(_ context.Context, bearer string, req sdk.CanRequest, items []sdk.BatchItem) ([]sdk.BatchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls++
	if f.Err != nil {
		return nil, f.Err
	}
	out := make([]sdk.BatchResult, len(items))
	for i, it := range items {
		out[i].Object = it.Object
		out[i].Relation = it.Relation
		out[i].Decision.Allowed = f.Grants[req.CompanyID+"|"+it.Object.Type+"|"+it.Object.ID+"|"+it.Relation+"|"+bearer]
	}
	return out, nil
}

func (f *Fake) MeCompanies(_ context.Context, bearer string) ([]sdk.Company, error) {
	return f.Companies[bearer], f.Err
}

// Grant records that subject holds relation on type/id in company.
func (f *Fake) Grant(company, typ, id, relation, subject string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Grants == nil {
		f.Grants = map[string]bool{}
	}
	f.Grants[company+"|"+typ+"|"+id+"|"+relation+"|"+subject] = true
}

// Revoke removes a grant.
func (f *Fake) Revoke(company, typ, id, relation, subject string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Grants, company+"|"+typ+"|"+id+"|"+relation+"|"+subject)
}
