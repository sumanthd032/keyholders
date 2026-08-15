package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const ecosystemsNames = "https://packages.ecosyste.ms/api/v1/registries/npmjs.org/package_names"

// namesPerPage is the page size used for the ranked name list. Measured at 1,000 names per 17.5 KB
// in 0.64 s, so 50,000 names costs about 1 MB and under a minute.
const namesPerPage = 1000

const namesMaxAge = 7 * 24 * time.Hour

// TopPackages returns the n most downloaded npm package names, most downloaded first.
//
// The ranking comes from ecosyste.ms rather than npm because npm's own bulk download endpoint
// refuses scoped packages, and scoped names are 44% of the top 50,000. Ranking through npm would
// therefore have silently omitted every @types, @babel and @aws-sdk package, which is most of the
// load-bearing part of the ecosystem.
//
// The sibling `packages` endpoint returns the same ranking with full metadata at roughly 10 MB per
// 100 packages, which is 5 GB for this list. `package_names` returns bare strings instead.
func (c *Client) TopPackages(ctx context.Context, n int) ([]string, error) {
	names := make([]string, 0, n)
	seen := make(map[string]bool, n)

	for page := 1; len(names) < n; page++ {
		u := fmt.Sprintf("%s?sort=downloads&order=desc&per_page=%d&page=%d", ecosystemsNames, namesPerPage, page)
		body, err := c.Get(ctx, u, namesMaxAge)
		if err != nil {
			return nil, fmt.Errorf("ecosyste.ms page %d: %w", page, err)
		}

		var batch []string
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("parse ecosyste.ms page %d: %w", page, err)
		}
		if len(batch) == 0 {
			break
		}

		for _, name := range batch {
			// Paging a live ranking can repeat a name if the underlying order shifts between
			// requests. Measured as zero overlap across pages 1, 2, 25, 50 and 51, but a duplicate
			// package would become a duplicate ingest unit, so it is cheap to rule out here.
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
			if len(names) == n {
				break
			}
		}
	}
	return names, nil
}
