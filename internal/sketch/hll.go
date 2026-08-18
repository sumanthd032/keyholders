package sketch

import (
	"math"
	"math/bits"
)

// HLL is a HyperLogLog cardinality estimator: a fixed size sketch that estimates the number of
// distinct members added to it, mergeable with another HLL of the same precision by taking the
// register-wise maximum. Precision trades memory for accuracy: each doubling of the register count
// roughly halves the standard error, at the cost of double the memory per sketch.
type HLL struct {
	precision uint
	registers []byte
}

// NewHLL constructs an empty HyperLogLog with 1<<precision registers.
func NewHLL(precision uint) *HLL {
	return &HLL{precision: precision, registers: make([]byte, 1<<precision)}
}

// Add absorbs one member. Its 64 bit hash is split into a register index, the top `precision` bits,
// and a rank, the number of leading zeros in the remaining bits plus one. A member landing in a
// register with a higher rank than anything seen there before raises that register; nothing else
// about the sketch changes, which is what makes Add and Merge the same underlying operation.
func (h *HLL) Add(n NodeID) {
	x := hash64(n)
	idx := x >> (64 - h.precision)
	rank := uint8(bits.LeadingZeros64(x<<h.precision) + 1)
	if rank > h.registers[idx] {
		h.registers[idx] = rank
	}
}

// hash64 hashes a member deterministically: the same string always lands in the same register with
// the same rank, in this run and in every other one. Determinism is what makes the error curve
// measured in hll_test.go reproducible instead of depending on a per-process random seed, and
// nothing about mergeability needs unpredictability, so there is no reason to pay for it.
//
// FNV-1a alone is not enough. The estimator reads leading zeros off the raw top bits of the hash,
// which means every one of those bits has to be close to independent and unbiased, and measuring
// this implementation against known cardinalities (see hll_test.go) showed FNV-1a badly failing
// that on short, near identical keys such as "member-1" through "member-100000": estimates came out
// an order of magnitude low because too few of its top bits had finished mixing by the time the
// short input ran out. Finishing the mix with the finalizer from MurmurHash3's 64 bit variant,
// three xorshift and multiply rounds, fixed it; the same measurement afterward tracked the nominal
// standard error at every precision tested.
func hash64(n NodeID) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(n); i++ {
		h ^= uint64(n[i])
		h *= prime64
	}

	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}

// Merge absorbs another HLL by taking the register-wise maximum, which is exact set union under
// estimation: a member present in either sketch raised its register in at least one of them, and the
// maximum keeps whichever raised it further. other must be an *HLL of the same precision; mixing
// precisions would silently under merge whichever sketch has fewer registers, which is a worse
// failure than refusing to run.
func (h *HLL) Merge(other Sketch) {
	o := other.(*HLL)
	if o.precision != h.precision {
		panic("HLL.Merge: mismatched precision")
	}
	for i, r := range o.registers {
		if r > h.registers[i] {
			h.registers[i] = r
		}
	}
}

// Count returns the estimated number of distinct members added or merged into this sketch, using
// the original HyperLogLog estimator with small range linear counting correction. A 64 bit hash puts
// the large range correction (needed only as cardinality approaches the hash space size) far outside
// anything this project ever counts, so it is not implemented.
func (h *HLL) Count() int {
	m := float64(len(h.registers))
	sum := 0.0
	zeros := 0
	for _, r := range h.registers {
		sum += math.Pow(2, -float64(r))
		if r == 0 {
			zeros++
		}
	}

	estimate := alpha(m) * m * m / sum
	if estimate <= 2.5*m && zeros > 0 {
		// Few enough distinct members that collisions in the empty registers dominate the error;
		// linear counting is more accurate here than the HLL estimator itself.
		return int(math.Round(m * math.Log(m/float64(zeros))))
	}
	return int(math.Round(estimate))
}

// alpha is the bias correction constant from the original HyperLogLog paper. The three small cases
// are measured constants that do not fit the asymptotic formula; anything at or above the
// observatory's own precision uses the formula directly.
func alpha(m float64) float64 {
	switch m {
	case 16:
		return 0.673
	case 32:
		return 0.697
	case 64:
		return 0.709
	default:
		return 0.7213 / (1 + 1.079/m)
	}
}
