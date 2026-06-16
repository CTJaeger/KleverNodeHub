package klever

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockChain serves canned indexer/node API responses for a tiny 5-block window.
// Validator "aa" is managed; "bb" is some other producer.
func mockChain(t *testing.T) *httptest.Server {
	t.Helper()
	// nonce -> (producerBLS, validators array json)
	blocks := map[uint64]struct {
		producer string
		signers  string
	}{
		1: {"bb", `[]`},          // empty signer set -> idle (can't attribute a miss)
		2: {"aa", `["aa","bb"]`}, // aa produced
		3: {"bb", `["bb"]`},      // aa elected but absent -> missed
		4: {"bb", `["aa","bb"]`}, // aa signed -> idle
		5: {"aa", `["aa","bb"]`}, // aa produced
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/node/overview", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":{"overview":{"epochNumber":42,"nonce":5,"currentSlot":500,"slotsPerEpoch":1000,"slotDuration":4000}}}`)
	})
	mux.HandleFunc("/v1.0/block/by-nonce/", func(w http.ResponseWriter, r *http.Request) {
		var n uint64
		parts := strings.Split(strings.TrimRight(r.URL.Path, "/"), "/")
		_, _ = fmt.Sscanf(parts[len(parts)-1], "%d", &n)
		b, ok := blocks[n]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"data":{"block":{"nonce":%d,"slot":%d,"epoch":42,"producerBLS":%q,"validators":%s}}}`,
			n, n, b.producer, b.signers)
	})
	mux.HandleFunc("/v1.0/validator/list", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			_, _ = fmt.Fprint(w, `{"data":{"validators":[]}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"validators":[
			{"ownerAddress":"klv1own","blsPublicKey":"aa","name":"Community-Node-1","list":"elected",
			 "commission":3000,"selfStake":5000000,"accumulatedFees":1500000,
			 "leaderSuccessRate":{"numSuccess":2,"numFailure":1},
			 "validatorSuccessRate":{"numSuccess":3,"numFailure":1}},
			{"ownerAddress":"klv1other","blsPublicKey":"bb","name":"Other","list":"elected"}
		]}}`)
	})
	return httptest.NewServer(mux)
}

func TestMonitor_TimelineAndSummary(t *testing.T) {
	srv := mockChain(t)
	defer srv.Close()

	client := NewClient(srv.URL, srv.URL, 4)
	// Managed node uses 0x + uppercase to exercise BLS normalization.
	nodes := func() []ManagedNode {
		return []ManagedNode{{BLS: "0xAA", Name: "my-node"}}
	}
	m := NewMonitor(client, nodes, "mainnet", 5, 0)
	m.tick(context.Background())

	snap := m.Snapshot()
	if !snap.Ready {
		t.Fatalf("snapshot not ready: %q", snap.Error)
	}
	if snap.Epoch != 42 || snap.HeadNonce != 5 {
		t.Fatalf("epoch/head = %d/%d, want 42/5", snap.Epoch, snap.HeadNonce)
	}
	if len(snap.Validators) != 1 {
		t.Fatalf("expected 1 managed validator, got %d", len(snap.Validators))
	}
	v := snap.Validators[0]

	// On-chain name overrides the local node label once matched.
	if v.Name != "Community-Node-1" {
		t.Errorf("name = %q, want Community-Node-1", v.Name)
	}
	if v.State != "elected" {
		t.Errorf("state = %q, want elected", v.State)
	}
	if v.Commission != 30 { // 3000 basis points -> 30%
		t.Errorf("commission = %v, want 30", v.Commission)
	}
	if v.SelfStake != 5.0 { // 5000000 / 1e6
		t.Errorf("self_stake = %v, want 5", v.SelfStake)
	}
	if v.Produced != 2 || v.Missed != 1 || v.Signed != 3 || v.LeaderMisses != 1 {
		t.Errorf("counts produced/missed/signed/leaderMisses = %d/%d/%d/%d, want 2/1/3/1",
			v.Produced, v.Missed, v.Signed, v.LeaderMisses)
	}

	// Timeline ascending by nonce (1..5): idle, produced, missed, idle, produced.
	want := []string{"idle", "produced", "missed", "idle", "produced"}
	if len(v.Timeline) != len(want) {
		t.Fatalf("timeline len = %d, want %d", len(v.Timeline), len(want))
	}
	for i, cell := range v.Timeline {
		if cell.Nonce != uint64(i+1) {
			t.Errorf("cell %d nonce = %d, want %d", i, cell.Nonce, i+1)
		}
		if cell.Status != want[i] {
			t.Errorf("cell %d (nonce %d) status = %q, want %q", i, cell.Nonce, cell.Status, want[i])
		}
	}

	// Summary.
	if snap.Summary.Managed != 1 || snap.Summary.Elected != 1 || snap.Summary.Jailed != 0 {
		t.Errorf("summary managed/elected/jailed = %d/%d/%d, want 1/1/0",
			snap.Summary.Managed, snap.Summary.Elected, snap.Summary.Jailed)
	}
	if snap.Summary.Produced != 2 || snap.Summary.Missed != 1 {
		t.Errorf("summary produced/missed = %d/%d, want 2/1", snap.Summary.Produced, snap.Summary.Missed)
	}
	if snap.Summary.TotalStaking != 5.0 || snap.Summary.TotalAllowance != 1.5 {
		t.Errorf("summary staking/allowance = %v/%v, want 5/1.5", snap.Summary.TotalStaking, snap.Summary.TotalAllowance)
	}
}

func TestMonitor_UnmatchedNodeIsOffChain(t *testing.T) {
	srv := mockChain(t)
	defer srv.Close()

	client := NewClient(srv.URL, srv.URL, 4)
	nodes := func() []ManagedNode {
		return []ManagedNode{{BLS: "ff", Name: "ghost"}} // not in validator list
	}
	m := NewMonitor(client, nodes, "mainnet", 5, 0)
	m.tick(context.Background())

	snap := m.Snapshot()
	if len(snap.Validators) != 1 {
		t.Fatalf("expected 1 row, got %d", len(snap.Validators))
	}
	v := snap.Validators[0]
	if v.OnChain {
		t.Errorf("unmatched node should be off-chain")
	}
	if v.Name != "ghost" {
		t.Errorf("name = %q, want fallback ghost", v.Name)
	}
	// Off-chain validator never produced/missed in the window -> all idle.
	for _, c := range v.Timeline {
		if c.Status != "idle" {
			t.Errorf("nonce %d status = %q, want idle", c.Nonce, c.Status)
		}
	}
}

func TestClient_ParsesValidatorList(t *testing.T) {
	srv := mockChain(t)
	defer srv.Close()
	client := NewClient(srv.URL, srv.URL, 4)

	vals, err := client.Validators(context.Background())
	if err != nil {
		t.Fatalf("Validators: %v", err)
	}
	if len(vals) != 2 {
		t.Fatalf("got %d validators, want 2", len(vals))
	}
	if vals[0].BLSPublicKey != "aa" || vals[0].LeaderSuccessRate.NumSuccess != 2 {
		t.Errorf("parse mismatch: %+v", vals[0])
	}
}

func TestNormalizeBLS(t *testing.T) {
	cases := map[string]string{
		"0xAB":  "ab",
		"AB":    "ab",
		"  Ab ": "ab",
		"0xabc": "abc",
	}
	for in, want := range cases {
		if got := normalizeBLS(in); got != want {
			t.Errorf("normalizeBLS(%q) = %q, want %q", in, got, want)
		}
	}
}
