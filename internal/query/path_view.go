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

func NewPathView(handle string, held []string, chains []PackageChain) PathView {
	view := PathView{Handle: handle, Holds: held}
	for _, c := range chains {
		view.Chains = append(view.Chains, NewChainView(c))
	}
	return view
}
