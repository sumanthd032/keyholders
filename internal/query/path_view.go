package query

// HopView is a Hop shaped for the wire.
type HopView struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Range     string `json:"range"`
	ValidFrom int64  `json:"valid_from"`
	ValidTo   int64  `json:"valid_to"`
}

// ChainView is a PackageChain shaped for the wire. Hops and the validity span are only present when
// Found, since an absent chain has nothing to project.
type ChainView struct {
	Package   string    `json:"package"`
	Found     bool      `json:"found"`
	ValidFrom int64     `json:"valid_from,omitempty"`
	ValidTo   int64     `json:"valid_to,omitempty"`
	Hops      []HopView `json:"hops,omitempty"`
}

func NewChainView(c PackageChain) ChainView {
	view := ChainView{Package: c.Package, Found: c.Found}
	if !c.Found {
		return view
	}
	view.ValidFrom, view.ValidTo = c.Chain.Valid.From, c.Chain.Valid.To
	for _, h := range c.Chain.Hops {
		view.Hops = append(view.Hops, HopView{
			From: h.From, To: h.To, Range: h.Range,
			ValidFrom: h.Valid.From, ValidTo: h.Valid.To,
		})
	}
	return view
}

// PathView is the proof-path answer for one handle: every package they hold in this audit, and the
// shortest coexistence chain to each, or its absence. The CLI's path command and the audit path API
// endpoint both build this same shape, so a proof looks identical whether read from a terminal or a
// browser.
type PathView struct {
	Handle string      `json:"handle"`
	Holds  []string    `json:"holds"`
	Chains []ChainView `json:"chains"`
}

// Both slices are built empty rather than left nil, so a handle that holds nothing serializes as []
// and not null. The browser indexes chains directly to read the first proof, and a null there threw
// before it could check for emptiness, taking the whole render down instead of showing "no chain".
func NewPathView(handle string, held []string, chains []PackageChain) PathView {
	view := PathView{
		Handle: handle,
		Holds:  make([]string, 0, len(held)),
		Chains: make([]ChainView, 0, len(chains)),
	}
	view.Holds = append(view.Holds, held...)
	for _, c := range chains {
		view.Chains = append(view.Chains, NewChainView(c))
	}
	return view
}
