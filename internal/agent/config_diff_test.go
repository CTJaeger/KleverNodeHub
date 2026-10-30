package agent

import (
	"strings"
	"testing"
)

const apiYAMLMine = `apiPackages:
  node:
    routes:
      - name: /status
        open: true
      # keep behind auth
      - name: /heartbeatstatus
        open: true
        secured: false
      - name: /custom
        open: true
  log:
    routes:
      - name: /log
        open: true
        secured: true
credentials:
  - username: example
    password: ""
hasher:
  type: sha256
`

const apiYAMLNew = `apiPackages:
  node:
    routes:
      - name: /status
        open: true
      # keep behind auth
      - name: /heartbeatstatus
        open: true
        secured: true
      - name: /metrics
        open: true
  log:
    routes:
      - name: /log
        open: true
        secured: true
credentials:
  - username: example
    password: ""
hasher:
  type: sha256
`

func findItem(t *testing.T, items []ConfigDiffItem, key string) ConfigDiffItem {
	t.Helper()
	for _, it := range items {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("no diff item with key %q in %+v", key, items)
	return ConfigDiffItem{}
}

func entryItems(entries []diffEntry) []ConfigDiffItem {
	out := make([]ConfigDiffItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.item)
	}
	return out
}

func TestDiffConfigFile_YAMLChangedAndLocalOnly(t *testing.T) {
	entries, added := diffConfigFile("api.yaml", apiYAMLMine, apiYAMLNew)
	items := entryItems(entries)

	changed := findItem(t, items, "apiPackages/node/routes[/heartbeatstatus]/secured")
	if changed.Kind != diffKindChanged || changed.Mine != "false" || changed.New != "true" {
		t.Errorf("unexpected changed item: %+v", changed)
	}

	local := findItem(t, items, "apiPackages/node/routes[/custom]")
	if local.Kind != diffKindLocalOnly {
		t.Errorf("expected local_only, got %+v", local)
	}
	if !strings.Contains(local.Mine, "- name: /custom") || !strings.Contains(local.Mine, "open: true") {
		t.Errorf("local_only preview should show the entry, got %q", local.Mine)
	}

	if len(items) != 2 {
		t.Errorf("expected exactly 2 items, got %d: %+v", len(items), items)
	}
	// /metrics (2 leaves: name + open) is new in the download and just taken.
	if added != 2 {
		t.Errorf("expected 2 added leaves, got %d", added)
	}
}

func TestApplyConfigKeeps_YAMLKeepsOnlyChosen(t *testing.T) {
	// Nothing chosen: the download is returned byte for byte.
	got, applied, _ := applyConfigKeeps("api.yaml", apiYAMLMine, apiYAMLNew, map[string]bool{})
	if got != apiYAMLNew || len(applied) != 0 {
		t.Fatalf("no keep must return the download unchanged")
	}

	keep := map[string]bool{"api.yaml::apiPackages/node/routes[/heartbeatstatus]/secured": true}
	got, applied, missed := applyConfigKeeps("api.yaml", apiYAMLMine, apiYAMLNew, keep)
	if len(applied) != 1 || len(missed) != 0 {
		t.Fatalf("applied=%v missed=%v", applied, missed)
	}
	want := strings.Replace(apiYAMLNew, "secured: true\n      - name: /metrics", "secured: false\n      - name: /metrics", 1)
	if got != want {
		t.Errorf("only the chosen value may change.\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestApplyConfigKeeps_YAMLReinsertsLocalEntry(t *testing.T) {
	keep := map[string]bool{"api.yaml::apiPackages/node/routes[/custom]": true}
	got, applied, _ := applyConfigKeeps("api.yaml", apiYAMLMine, apiYAMLNew, keep)
	if len(applied) != 1 {
		t.Fatalf("expected the local entry to be applied, got %v", applied)
	}
	want := strings.Replace(apiYAMLNew,
		"      - name: /metrics\n        open: true\n",
		"      - name: /metrics\n        open: true\n      - name: /custom\n        open: true\n", 1)
	if got != want {
		t.Errorf("local entry must be inserted after the last route.\n got:\n%s\nwant:\n%s", got, want)
	}

	// The result must itself be a clean state: re-diffing against the user's
	// file no longer reports the kept entry.
	again, _ := diffConfigFile("api.yaml", apiYAMLMine, got)
	for _, e := range again {
		if e.item.Key == "apiPackages/node/routes[/custom]" {
			t.Errorf("kept entry still reported after apply")
		}
	}
}

func TestApplyConfigKeeps_YAMLKeyInsideItemKeepsIndent(t *testing.T) {
	mine := "routes:\n  - name: /a\n    open: true\n    secured: false\n"
	fresh := "routes:\n  - name: /a\n    open: true\n"
	entries, _ := diffConfigFile("api.yaml", mine, fresh)
	if len(entries) != 1 || entries[0].item.Key != "routes[/a]/secured" {
		t.Fatalf("expected the missing secured key, got %+v", entryItems(entries))
	}
	got, _, _ := applyConfigKeeps("api.yaml", mine, fresh, map[string]bool{"api.yaml::routes[/a]/secured": true})
	if got != mine {
		t.Errorf("got:\n%s\nwant:\n%s", got, mine)
	}
}

func TestDiffConfigFile_TOMLIgnoresDisplayName(t *testing.T) {
	mine := "[Node]\n  NodeDisplayName = \"Mine\"\n  Port = 9000 # custom\n  Custom = true\n"
	fresh := "[Node]\n  NodeDisplayName = \"\"\n  Port = 8080\n"
	entries, _ := diffConfigFile("config.toml", mine, fresh)
	items := entryItems(entries)

	port := findItem(t, items, "Node/Port")
	if port.Mine != "9000" || port.New != "8080" {
		t.Errorf("unexpected port item: %+v", port)
	}
	findItem(t, items, "Node/Custom")
	for _, it := range items {
		if strings.Contains(it.Key, "NodeDisplayName") {
			t.Errorf("NodeDisplayName is handled separately and must not be offered")
		}
	}

	got, _, _ := applyConfigKeeps("config.toml", mine, fresh, map[string]bool{
		"config.toml::Node/Port":   true,
		"config.toml::Node/Custom": true,
	})
	want := "[Node]\n  NodeDisplayName = \"\"\n  Port = 9000 # custom\n  Custom = true\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestDiffConfigFile_TOMLMultilineArray(t *testing.T) {
	mine := "[A]\n  List = [\n    \"x\",\n    \"y\",\n  ]\n  Other = 1\n"
	fresh := "[A]\n  List = [\n    \"x\",\n    \"z\",\n  ]\n  Other = 1\n"
	entries, _ := diffConfigFile("config.toml", mine, fresh)
	if len(entries) != 1 || entries[0].item.Key != "A/List" {
		t.Fatalf("expected the array to be one changed value, got %+v", entryItems(entries))
	}
	got, _, _ := applyConfigKeeps("config.toml", mine, fresh, map[string]bool{"config.toml::A/List": true})
	if got != mine {
		t.Errorf("got:\n%q\nwant:\n%q", got, mine)
	}
}

func TestDiffConfigFile_JSONIsOneFileChoice(t *testing.T) {
	mine := "{\n  \"a\": 1\n}\n"
	fresh := "{\"a\":2}"

	if entries, _ := diffConfigFile("genesis.json", mine, "{\"a\":1}"); len(entries) != 0 {
		t.Errorf("formatting-only differences must not be reported")
	}

	entries, _ := diffConfigFile("genesis.json", mine, fresh)
	if len(entries) != 1 || entries[0].item.Kind != diffKindFile {
		t.Fatalf("expected one file-level item, got %+v", entryItems(entries))
	}
	got, applied, _ := applyConfigKeeps("genesis.json", mine, fresh, map[string]bool{"genesis.json::" + wholeFileKey: true})
	if got != mine || len(applied) != 1 {
		t.Errorf("keeping the file must restore the local content, got %q", got)
	}
	got, _, _ = applyConfigKeeps("genesis.json", mine, fresh, map[string]bool{})
	if got != fresh {
		t.Errorf("without a choice the download wins, got %q", got)
	}
}

func TestApplyConfigKeeps_StaleIDIsReported(t *testing.T) {
	got, applied, missed := applyConfigKeeps("api.yaml", apiYAMLMine, apiYAMLNew,
		map[string]bool{"api.yaml::does/not/exist": true})
	if got != apiYAMLNew || len(applied) != 0 || len(missed) != 1 {
		t.Errorf("stale id must leave the download untouched and be reported: applied=%v missed=%v", applied, missed)
	}
}

func TestParseYAMLConfig_BlockScalarAndFlow(t *testing.T) {
	text := "a:\n  msg: |\n    line one\n    line two\n  list: [1,\n    2]\n  next: x\n"
	root := parseYAMLConfig(text)
	_, by := flattenConfig(root)
	if n := by["/a/next"]; n == nil || n.value != "x" {
		t.Fatalf("keys after block scalar / multi-line flow must still parse, got %+v", n)
	}
	if n := by["/a/msg"]; n == nil || n.end-n.line != 3 {
		t.Errorf("block scalar should span 3 lines, got %+v", n)
	}
}
