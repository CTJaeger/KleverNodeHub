package klever

import "time"

// TimelineCell is one block in a validator's rolling production timeline.
// Status is "produced" (this validator led the block), "missed" (the validator
// was elected but absent from the block's consensus signer set), or "idle"
// (someone else led and this validator either signed or wasn't due).
type TimelineCell struct {
	Nonce  uint64 `json:"nonce"`
	Status string `json:"status"`
}

// ValidatorView is one managed validator's row in the snapshot.
type ValidatorView struct {
	BLS          string         `json:"bls"`
	Name         string         `json:"name"`
	NodeName     string         `json:"node_name"`
	State        string         `json:"state"`
	OnChain      bool           `json:"on_chain"`
	Commission   float64        `json:"commission"` // percent
	SelfStake    float64        `json:"self_stake"` // KLV
	Allowance    float64        `json:"allowance"`  // KLV (accumulated/claimable fees)
	Produced     int64          `json:"produced"`   // blocks led this epoch
	LeaderMisses int64          `json:"leader_misses"`
	Signed       int64          `json:"signed"`
	Missed       int64          `json:"missed"`
	Timeline     []TimelineCell `json:"timeline"`
}

// Summary aggregates the managed validators for the stat cards.
type Summary struct {
	Managed        int     `json:"managed"`
	Elected        int     `json:"elected"`
	Jailed         int     `json:"jailed"`
	TotalStaking   float64 `json:"total_staking"`   // KLV
	TotalAllowance float64 `json:"total_allowance"` // KLV
	Produced       int64   `json:"produced"`
	Missed         int64   `json:"missed"`
}

// Snapshot is the full payload served to the validators page.
type Snapshot struct {
	Epoch      uint64          `json:"epoch"`
	HeadNonce  uint64          `json:"head_nonce"`
	Window     int             `json:"window"`
	Network    string          `json:"network"`
	UpdatedAt  int64           `json:"updated_at"`
	Ready      bool            `json:"ready"`
	Error      string          `json:"error,omitempty"`
	Summary    Summary         `json:"summary"`
	Validators []ValidatorView `json:"validators"`
}

func nowUnix() int64 { return time.Now().Unix() }
