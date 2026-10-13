// Package authz is Hareno's one integration point with Kiban (ARCHITECTURE-PLAN §10). It
// verifies users, resolves their companies, and answers object-relation questions through a
// decision cache (§10.4 item 13): allows are kept 30 s and denials 5 s, so removing access takes
// effect within 30 s; Kiban errors are never cached and always deny.
package authz

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rosschiu/kiban/sdk"
)

// Kiban is the subset of the Kiban SDK Hareno uses; *sdk.Client satisfies it.
type Kiban interface {
	VerifyUserToken(ctx context.Context, bearer string) (string, error)
	BatchCan(ctx context.Context, bearer string, req sdk.CanRequest, items []sdk.BatchItem) ([]sdk.BatchResult, error)
	MeCompanies(ctx context.Context, bearer string) ([]sdk.Company, error)
}

// Item is one question: may the user hold Relation on object Type/ID?
type Item struct{ Type, ID, Relation string }

// Authz answers for one Kiban app (Module) and feature.
type Authz struct {
	K        Kiban
	Module   string // e.g. "hareno"
	Feature  string // e.g. "hareno.use"
	AllowTTL time.Duration
	DenyTTL  time.Duration
	Now      func() time.Time // for tests; defaults to time.Now

	mu        sync.Mutex
	decisions map[decisionKey]cached
	companies map[string]cachedCompanies
}

// ErrUnauthenticated is returned for a missing, invalid or expired token.
var ErrUnauthenticated = errors.New("unauthenticated")

// ErrNoCompany is returned when the user is not an active member of the company.
var ErrNoCompany = errors.New("not a member of that company")

type decisionKey struct {
	company, sub string
	item         Item
}

type cached struct {
	allowed bool
	until   time.Time
}

type cachedCompanies struct {
	byCode map[string]string
	until  time.Time
}

func (a *Authz) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// User verifies a bearer token and returns its subject. Verification alone never grants data
// access (§10.4 item 14): callers must also ask Allowed.
func (a *Authz) User(ctx context.Context, bearer string) (string, error) {
	if bearer == "" {
		return "", ErrUnauthenticated
	}
	sub, err := a.K.VerifyUserToken(ctx, bearer)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	return sub, nil
}

// Company resolves a company code to its id for this user, if they are an active member.
// Cached for AllowTTL per user.
func (a *Authz) Company(ctx context.Context, bearer, sub, code string) (string, error) {
	a.mu.Lock()
	c, ok := a.companies[sub]
	a.mu.Unlock()
	if !ok || a.now().After(c.until) {
		list, err := a.K.MeCompanies(ctx, bearer)
		if err != nil {
			return "", fmt.Errorf("kiban: %w", err)
		}
		c = cachedCompanies{byCode: map[string]string{}, until: a.now().Add(a.AllowTTL)}
		for _, co := range list {
			if co.IsActive {
				c.byCode[co.Code] = co.ID
			}
		}
		a.mu.Lock()
		if a.companies == nil {
			a.companies = map[string]cachedCompanies{}
		}
		a.companies[sub] = c
		a.mu.Unlock()
	}
	id, ok := c.byCode[code]
	if !ok {
		return "", ErrNoCompany
	}
	return id, nil
}

// Allowed answers each item for the user in company: cached answers first, the rest in batches
// of up to 100. Any error denies everything asked.
func (a *Authz) Allowed(ctx context.Context, bearer, sub, company string, items []Item) (map[Item]bool, error) {
	out := make(map[Item]bool, len(items))
	var missing []Item
	now := a.now()
	a.mu.Lock()
	for _, it := range items {
		if c, ok := a.decisions[decisionKey{company, sub, it}]; ok && now.Before(c.until) {
			out[it] = c.allowed
		} else {
			missing = append(missing, it)
		}
	}
	a.mu.Unlock()

	req := sdk.CanRequest{FeatureKey: a.Feature, ModuleKey: a.Module, CompanyID: company}
	for len(missing) > 0 {
		chunk := missing[:min(100, len(missing))]
		missing = missing[len(chunk):]
		batch := make([]sdk.BatchItem, len(chunk))
		for i, it := range chunk {
			batch[i] = sdk.NewBatchItem(it.Type, it.ID, it.Relation)
		}
		res, err := a.K.BatchCan(ctx, bearer, req, batch)
		if err != nil {
			return nil, fmt.Errorf("kiban: %w", err)
		}
		if len(res) != len(chunk) {
			return nil, fmt.Errorf("kiban: %d answers for %d questions", len(res), len(chunk))
		}
		now := a.now()
		a.mu.Lock()
		if a.decisions == nil {
			a.decisions = map[decisionKey]cached{}
		}
		for i, r := range res {
			it := chunk[i]
			if r.Object.Type != it.Type || r.Object.ID != it.ID || r.Relation != it.Relation {
				a.mu.Unlock()
				return nil, errors.New("kiban: answers out of order")
			}
			ttl := a.DenyTTL
			if r.Decision.Allowed {
				ttl = a.AllowTTL
			}
			// ponytail: unbounded map; bound it (LRU) once the subject x object count matters.
			a.decisions[decisionKey{company, sub, it}] = cached{r.Decision.Allowed, now.Add(ttl)}
			out[it] = r.Decision.Allowed
		}
		a.mu.Unlock()
	}
	return out, nil
}
