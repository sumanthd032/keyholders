package sketch

// ExactSet is an uncompressed Sketch backed by a real set: no error, no fixed memory bound. It exists
// for two things HyperLogLog cannot do for itself: pinning a test case down to an exact integer
// instead of an error band, and giving a validation run something to compare HyperLogLog's estimate
// against that is not itself an estimate. Running Engine.RunEpoch with ExactSet computes the same
// fixpoint an exact BFS would, over whatever edges it is handed; it is not a smaller or cheaper
// version of HLL, it is what HLL is approximating.
type ExactSet struct{ members map[NodeID]bool }

// NewExactSet constructs an empty ExactSet as a Sketch, ready to pass as Engine.New.
func NewExactSet() Sketch { return &ExactSet{members: map[NodeID]bool{}} }

func (s *ExactSet) Add(n NodeID) { s.members[n] = true }

func (s *ExactSet) Merge(other Sketch) {
	o := other.(*ExactSet)
	for n := range o.members {
		s.members[n] = true
	}
}

func (s *ExactSet) Count() int { return len(s.members) }
