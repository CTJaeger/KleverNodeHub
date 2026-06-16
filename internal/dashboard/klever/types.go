// Package klever provides a thin client for the Klever blockchain indexer/node
// APIs and a background monitor that tracks block production for the validators
// managed by this NodeHub.
package klever

// The wire types below mirror the Klever indexer/node API JSON. Field names use
// the API's camelCase keys; a few keys (producerBLS, kAppFees) are irregular and
// tagged explicitly.

// overviewEnvelope wraps GET {nodeURL}/node/overview.
type overviewEnvelope struct {
	Data struct {
		Overview Overview `json:"overview"`
	} `json:"data"`
}

// Overview is the chain epoch/slot clock from the node API.
type Overview struct {
	EpochNumber       uint64 `json:"epochNumber"`
	Nonce             uint64 `json:"nonce"`
	CurrentSlot       uint64 `json:"currentSlot"`
	NonceAtEpochStart uint64 `json:"nonceAtEpochStart"`
	SlotAtEpochStart  uint64 `json:"slotAtEpochStart"`
	SlotsPerEpoch     uint64 `json:"slotsPerEpoch"`
	SlotDuration      uint64 `json:"slotDuration"`
}

// indexerBlockEnvelope wraps GET {apiURL}/v1.0/block/by-nonce/{nonce}.
type indexerBlockEnvelope struct {
	Data struct {
		Block IndexerBlock `json:"block"`
	} `json:"data"`
}

// IndexerBlock is a single block with its producer and ordered consensus group.
type IndexerBlock struct {
	Nonce uint64 `json:"nonce"`
	Slot  uint64 `json:"slot"`
	Epoch uint64 `json:"epoch"`
	// JSON key is `producerBLS` (capital BLS).
	ProducerBLS  string `json:"producerBLS"`
	ProducerName string `json:"producerName"`
	// Ordered consensus group BLS keys for this block (the signer set).
	Validators []string `json:"validators"`
}

// validatorListEnvelope wraps GET {apiURL}/v1.0/validator/list.
type validatorListEnvelope struct {
	Data struct {
		Validators []RawValidator `json:"validators"`
	} `json:"data"`
}

// successRate is Klever's {numSuccess, numFailure} per-epoch counter.
type successRate struct {
	NumSuccess int64 `json:"numSuccess"`
	NumFailure int64 `json:"numFailure"`
}

// RawValidator is one entry from the validator list.
type RawValidator struct {
	OwnerAddress    string `json:"ownerAddress"`
	BLSPublicKey    string `json:"blsPublicKey"`
	Name            string `json:"name"`
	List            string `json:"list"` // "elected", "waiting", "jailed", ...
	Commission      int64  `json:"commission"`
	SelfStake       int64  `json:"selfStake"`
	AccumulatedFees int64  `json:"accumulatedFees"`
	// Per-epoch blocks led: NumSuccess = produced, NumFailure = leader misses.
	LeaderSuccessRate successRate `json:"leaderSuccessRate"`
	// Per-epoch consensus signing: NumSuccess = signed, NumFailure = missed.
	ValidatorSuccessRate successRate `json:"validatorSuccessRate"`
}
