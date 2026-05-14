package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/CTJaeger/KleverNodeHub/internal/dashboard/ws"
	"github.com/CTJaeger/KleverNodeHub/internal/models"
	"github.com/CTJaeger/KleverNodeHub/internal/store"
)

const batchConfigTimeout = 30 * time.Second

// BatchConfigHandler handles bulk-config operations across multiple nodes.
type BatchConfigHandler struct {
	hub       *ws.Hub
	nodeStore *store.NodeStore
}

// NewBatchConfigHandler creates a new BatchConfigHandler.
func NewBatchConfigHandler(hub *ws.Hub, nodeStore *store.NodeStore) *BatchConfigHandler {
	return &BatchConfigHandler{hub: hub, nodeStore: nodeStore}
}

// parameterValue is one node's value for a config parameter.
type parameterValue struct {
	NodeID        string `json:"node_id"`
	ServerID      string `json:"server_id"`
	ContainerName string `json:"container_name"`
	Value         string `json:"value"`
}

// parameter is one config key with its values across all queried nodes.
type parameter struct {
	Key    string           `json:"key"`
	Values []parameterValue `json:"values"`
}

// nodeInfo is sent back to the frontend so it can display friendly names.
type nodeInfo struct {
	NodeID      string `json:"node_id"`
	DisplayName string `json:"display_name"`
	ServerID    string `json:"server_id"`
	ServerName  string `json:"server_name"`
}

// HandleListParameters fetches a config file from all online nodes and
// returns the union of parameters with their values per node.
// GET /api/batch-config/parameters?file=config.yaml
func (h *BatchConfigHandler) HandleListParameters(w http.ResponseWriter, r *http.Request) {
	file := r.URL.Query().Get("file")
	if file == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file query param required"})
		return
	}

	nodes, err := h.nodeStore.ListAll("")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	paramMap := make(map[string][]parameterValue)
	nodeInfos := make([]nodeInfo, 0, len(nodes))

	for i := range nodes {
		node := &nodes[i]
		nodeInfos = append(nodeInfos, nodeInfo{
			NodeID:      node.ID,
			DisplayName: nodeDisplayName(node),
			ServerID:    node.ServerID,
			ServerName:  node.ServerID,
		})

		if !h.hub.IsConnected(node.ServerID) {
			continue
		}
		if node.DataDirectory == "" {
			continue
		}

		content, err := h.readConfig(node, file)
		if err != nil {
			continue
		}
		parsed := parseFlatYAML(content)
		for key, value := range parsed {
			paramMap[key] = append(paramMap[key], parameterValue{
				NodeID:        node.ID,
				ServerID:      node.ServerID,
				ContainerName: node.ContainerName,
				Value:         value,
			})
		}
	}

	params := make([]parameter, 0, len(paramMap))
	for key, values := range paramMap {
		params = append(params, parameter{Key: key, Values: values})
	}
	sort.Slice(params, func(i, j int) bool { return params[i].Key < params[j].Key })

	writeJSON(w, http.StatusOK, map[string]any{
		"parameters": params,
		"nodes":      nodeInfos,
	})
}

// batchApplyRequest is the body for POST /api/batch-config/apply.
type batchApplyRequest struct {
	File         string        `json:"file"`
	Key          string        `json:"key"`
	Value        string        `json:"value"`
	RestartAfter bool          `json:"restart_after"`
	Targets      []batchTarget `json:"targets"`
}

type batchTarget struct {
	NodeID        string `json:"node_id"`
	ServerID      string `json:"server_id"`
	ContainerName string `json:"container_name"`
}

type batchApplyResult struct {
	NodeID  string `json:"node_id"`
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// HandleApply applies a new value for one config key across multiple nodes.
// POST /api/batch-config/apply
func (h *BatchConfigHandler) HandleApply(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Minute))

	var req batchApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.File == "" || req.Key == "" || len(req.Targets) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file, key, and targets required"})
		return
	}

	results := make([]batchApplyResult, 0, len(req.Targets))
	for _, t := range req.Targets {
		res := batchApplyResult{NodeID: t.NodeID}

		node, err := h.nodeStore.GetByID(t.NodeID)
		if err != nil {
			res.Message = "node not found"
			results = append(results, res)
			continue
		}
		if !h.hub.IsConnected(node.ServerID) {
			res.Message = "agent offline"
			results = append(results, res)
			continue
		}

		content, err := h.readConfig(node, req.File)
		if err != nil {
			res.Message = "read failed: " + err.Error()
			results = append(results, res)
			continue
		}

		updated, replaced := replaceFlatYAMLValue(content, req.Key, req.Value)
		if !replaced {
			res.Message = "key not found"
			results = append(results, res)
			continue
		}

		payload := map[string]string{
			"data_dir":  node.DataDirectory,
			"file_name": req.File,
			"content":   updated,
		}
		if req.RestartAfter {
			payload["restart_container"] = node.ContainerName
		}

		msg := &models.Message{
			ID:        fmt.Sprintf("cmd-config.write-%d-%s", time.Now().UnixNano(), node.ID),
			Type:      "command",
			Action:    "config.write",
			Payload:   payload,
			Timestamp: time.Now().Unix(),
		}
		cmdResult, err := h.hub.SendCommand(node.ServerID, msg, batchConfigTimeout)
		if err != nil {
			res.Message = err.Error()
			results = append(results, res)
			continue
		}
		if !cmdResult.Success {
			res.Message = cmdResult.Error
			results = append(results, res)
			continue
		}

		res.Success = true
		res.Message = "applied"
		if req.RestartAfter {
			res.Message = "applied + restarted"
		}
		results = append(results, res)
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// readConfig fetches a config file from the node's agent.
func (h *BatchConfigHandler) readConfig(node *models.Node, file string) (string, error) {
	if node.DataDirectory == "" {
		return "", fmt.Errorf("node has no data_directory")
	}
	msg := &models.Message{
		ID:        fmt.Sprintf("cmd-config.read-%d-%s", time.Now().UnixNano(), node.ID),
		Type:      "command",
		Action:    "config.read",
		Payload:   map[string]string{"data_dir": node.DataDirectory, "file_name": file},
		Timestamp: time.Now().Unix(),
	}
	result, err := h.hub.SendCommand(node.ServerID, msg, batchConfigTimeout)
	if err != nil {
		return "", err
	}
	if !result.Success {
		return "", fmt.Errorf("%s", result.Error)
	}
	return result.Output, nil
}

func nodeDisplayName(n *models.Node) string {
	if n.DisplayName != "" {
		return n.DisplayName
	}
	return n.ContainerName
}

// parseFlatYAML walks a YAML file and returns a flat map of dotted-path → value.
// It is intentionally simple: it tracks indentation to build the path and only
// emits leaf nodes (lines like "key: value" where value is not empty).
// Comments and array entries are skipped.
var (
	yamlLineRe    = regexp.MustCompile(`^(\s*)([A-Za-z_][A-Za-z0-9_\-]*)\s*:\s*(.*?)\s*(?:#.*)?$`)
	yamlCommentRe = regexp.MustCompile(`(^|\s)#.*$`)
)

func parseFlatYAML(content string) map[string]string {
	out := make(map[string]string)
	type stackEntry struct {
		indent int
		name   string
	}
	var stack []stackEntry

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "-") {
			continue // skip array entries for v1
		}
		m := yamlLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		indent := len(m[1])
		key := m[2]
		value := strings.TrimSpace(yamlCommentRe.ReplaceAllString(m[3], ""))

		// Pop stack until we find an ancestor with smaller indent
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		if value == "" {
			// section header — push to stack
			stack = append(stack, stackEntry{indent: indent, name: key})
			continue
		}

		path := key
		if len(stack) > 0 {
			parts := make([]string, 0, len(stack)+1)
			for _, e := range stack {
				parts = append(parts, e.name)
			}
			parts = append(parts, key)
			path = strings.Join(parts, ".")
		}
		out[path] = value
	}
	return out
}

// replaceFlatYAMLValue finds a line matching the last path component and
// replaces its value, preserving leading whitespace and inline comments.
// Returns the new content and a bool indicating whether a replacement happened.
func replaceFlatYAMLValue(content, dottedKey, newValue string) (string, bool) {
	parts := strings.Split(dottedKey, ".")
	if len(parts) == 0 {
		return content, false
	}
	leaf := parts[len(parts)-1]
	leafRe := regexp.MustCompile(`^(\s*)` + regexp.QuoteMeta(leaf) + `(\s*:\s*)(\S+)(.*)$`)

	lines := strings.Split(content, "\n")
	type stackEntry struct {
		indent int
		name   string
	}
	var stack []stackEntry
	replaced := false

	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "-") {
			continue
		}
		m := yamlLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		indent := len(m[1])
		key := m[2]
		value := strings.TrimSpace(yamlCommentRe.ReplaceAllString(m[3], ""))

		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		if value == "" {
			stack = append(stack, stackEntry{indent: indent, name: key})
			continue
		}

		// Build current dotted path
		path := key
		if len(stack) > 0 {
			pp := make([]string, 0, len(stack)+1)
			for _, e := range stack {
				pp = append(pp, e.name)
			}
			pp = append(pp, key)
			path = strings.Join(pp, ".")
		}
		if path != dottedKey {
			continue
		}

		newLine := leafRe.ReplaceAllString(line, "${1}"+leaf+"${2}"+newValue+"${4}")
		lines[i] = newLine
		replaced = true
		break // replace first occurrence; flat YAML doesn't have duplicates at same path
	}
	return strings.Join(lines, "\n"), replaced
}
