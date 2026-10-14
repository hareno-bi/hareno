package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/rosschiu/kiban/sdk"
)

// Directory searches a company's member directory as the user: Kiban's least-disclosure source
// for "share with..." pickers (any active member may read it). The Go SDK reads it only as the
// service account, which is not a member of client companies.
type Directory struct {
	Gateway string
	HTTP    *http.Client // defaults to http.DefaultClient
}

// Search returns up to 20 active members whose display name or email contains q.
func (d Directory) Search(ctx context.Context, bearer, companyID, q string) ([]sdk.DirectoryEntry, error) {
	u := d.Gateway + "/api/org/companies/" + url.PathEscape(companyID) + "/members?" + url.Values{"q": {q}, "pageSize": {"20"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	c := d.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kiban member directory: %d %s", resp.StatusCode, raw)
	}
	var env struct {
		Data sdk.DirectoryPage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	return env.Data.Items, nil
}
