package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/agent"
	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

const slotInspectorTimeout = 30 * time.Second

// SlotInspectorHandler handles slot-specific log queries.
type SlotInspectorHandler struct {
	hub       *ws.Hub
	nodeStore *store.NodeStore
}

// NewSlotInspectorHandler creates a new SlotInspectorHandler.
func NewSlotInspectorHandler(hub *ws.Hub, nodeStore *store.NodeStore) *SlotInspectorHandler {
	return &SlotInspectorHandler{hub: hub, nodeStore: nodeStore}
}

type slotInspectRequest struct {
	Slots   []string `json:"slots"`
	Context int      `json:"context"`
	Tail    int      `json:"tail"`
	NodeIDs []string `json:"node_ids"`
}

type slotTiming struct {
	Slot       string `json:"slot"`
	TimeMs     string `json:"time_ms"`
	LowerBound string `json:"lower_bound,omitempty"`
}

type slotInspectResult struct {
	NodeID   string       `json:"node_id"`
	NodeName string       `json:"node_name"`
	Lines    []string     `json:"lines"`
	Timings  []slotTiming `json:"timings,omitempty"`
	Error    string       `json:"error,omitempty"`
}

var (
	validatorTimeRe = regexp.MustCompile(`validatorTime\s*=\s*(\S+)`)
	lowerBoundRe    = regexp.MustCompile(`lowerBound\s*=\s*(\S+)`)
)

// HandleInspect handles POST /api/slot-inspect.
func (h *SlotInspectorHandler) HandleInspect(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(2 * time.Minute))

	var req slotInspectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if len(req.Slots) == 0 || len(req.NodeIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "slots and node_ids required"})
		return
	}
	context := req.Context
	if context <= 0 || context > 200 {
		context = 30
	}
	tail := req.Tail
	if tail <= 0 {
		tail = 10000
	}
	if tail > 100000 {
		tail = 100000
	}

	// Compile patterns for each slot
	slotPatterns := make([]*regexp.Regexp, 0, len(req.Slots))
	for _, s := range req.Slots {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, err := strconv.ParseInt(s, 10, 64); err != nil {
			continue
		}
		slotPatterns = append(slotPatterns, regexp.MustCompile(`SLOT\s+`+regexp.QuoteMeta(s)+`\b`))
	}
	if len(slotPatterns) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no valid slot numbers"})
		return
	}

	results := make([]slotInspectResult, 0, len(req.NodeIDs))
	for _, nodeID := range req.NodeIDs {
		res := slotInspectResult{NodeID: nodeID}

		node, err := h.nodeStore.GetByID(nodeID)
		if err != nil {
			res.NodeName = nodeID
			res.Error = "node not found"
			results = append(results, res)
			continue
		}
		res.NodeName = node.ContainerName
		if node.DisplayName != "" {
			res.NodeName = node.DisplayName
		}

		if !h.hub.IsConnected(node.ServerID) {
			res.Error = "agent offline"
			results = append(results, res)
			continue
		}

		// Fetch logs (tail configurable from the frontend, capped at 100000).
		msg := &models.Message{
			ID:     fmt.Sprintf("cmd-slotinspect-logs-%d-%s", time.Now().UnixNano(), node.ID),
			Type:   "command",
			Action: "node.logs",
			Payload: map[string]any{
				"container_name": node.ContainerName,
				"tail":           float64(tail),
			},
			Timestamp: time.Now().Unix(),
		}
		cmdResult, err := h.hub.SendCommand(node.ServerID, msg, slotInspectorTimeout)
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		if !cmdResult.Success {
			res.Error = cmdResult.Error
			results = append(results, res)
			continue
		}

		var logLines []agent.LogLine
		if err := json.Unmarshal([]byte(cmdResult.Output), &logLines); err != nil {
			res.Error = "parse logs failed: " + err.Error()
			results = append(results, res)
			continue
		}

		// Find all matching slot lines and grab `context` lines after each
		extracted := extractSlotContext(logLines, slotPatterns, context)
		res.Lines = extracted
		res.Timings = extractTimings(extracted, req.Slots)
		results = append(results, res)
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// extractSlotContext returns lines matching any slot pattern plus N following lines.
func extractSlotContext(lines []agent.LogLine, patterns []*regexp.Regexp, context int) []string {
	var out []string
	emitted := make(map[int]bool)

	for i, line := range lines {
		msg := strings.TrimSpace(line.Message)
		matched := false
		for _, p := range patterns {
			if p.MatchString(msg) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}

		// Emit this line and the next `context` lines (skip duplicates)
		end := i + context
		if end >= len(lines) {
			end = len(lines) - 1
		}
		for j := i; j <= end; j++ {
			if emitted[j] {
				continue
			}
			emitted[j] = true
			ts := lines[j].Timestamp
			body := strings.TrimRight(lines[j].Message, "\r\n")
			if ts != "" {
				out = append(out, ts+" "+body)
			} else {
				out = append(out, body)
			}
		}
		out = append(out, "")
	}
	// Drop trailing empty line
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// extractTimings parses validatorTime values from extracted lines.
func extractTimings(lines []string, slots []string) []slotTiming {
	var timings []slotTiming
	currentSlot := ""
	slotLineRe := regexp.MustCompile(`SLOT\s+(\d+)`)

	for _, l := range lines {
		if m := slotLineRe.FindStringSubmatch(l); m != nil {
			currentSlot = m[1]
		}
		if vm := validatorTimeRe.FindStringSubmatch(l); vm != nil {
			t := slotTiming{Slot: currentSlot, TimeMs: vm[1]}
			if lb := lowerBoundRe.FindStringSubmatch(l); lb != nil {
				t.LowerBound = lb[1]
			}
			timings = append(timings, t)
		}
	}
	return timings
}
