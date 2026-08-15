package graph

import "time"

// OpenInterval is the value stored in valid_to for an interval that has not ended: a maintainer who
// still holds the key, a resolution still in force.
//
// A sentinel is used rather than leaving the property unset because the supported Cypher subset has
// no IS NULL in WHERE. With a sentinel, "was this edge live at t" stays a plain range comparison,
// `valid_from <= t AND valid_to > t`, with no special case for the open end. The value is
// 2100-01-01 in Unix seconds: far enough out to be unreachable, small enough to read as a date in
// query output rather than as a magic number.
const OpenInterval int64 = 4102444800

// Timestamp converts an instant to the storage representation. Property values are integers,
// floats, booleans or strings, so times are Unix seconds. A zero time becomes 0, which sorts before
// every real publish and so never satisfies an interval containment test by accident.
func Timestamp(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
