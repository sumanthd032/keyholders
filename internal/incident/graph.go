package incident

import "github.com/neo4j/neo4j-go-driver/v5/neo4j"

// stringColumn extracts one string projection from every record, skipping a row whose value is
// absent or not a string rather than failing the whole read: a name column with nothing behind it is
// not something any query in this package has produced in practice, but silently returning it as a
// zero value would misreport an account's holdings as smaller than they are.
func stringColumn(recs []*neo4j.Record, column string) []string {
	out := make([]string, 0, len(recs))
	for _, rec := range recs {
		v, ok := rec.Get(column)
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		out = append(out, s)
	}
	return out
}
