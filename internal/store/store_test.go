package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/models"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// --- Database Tests ---

func TestOpenCreatesFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Open twice — migrations should run idempotently
	db1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	db1.Close()

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	db2.Close()
}

func TestWALMode(t *testing.T) {
	db := newTestDB(t)

	var mode string
	err := db.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}
}

// --- Settings Tests ---

func TestSettingsSetAndGet(t *testing.T) {
	db := newTestDB(t)
	ss := NewSettingsStore(db)

	if err := ss.Set("key1", "value1"); err != nil {
		t.Fatalf("set: %v", err)
	}

	val, err := ss.Get("key1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != "value1" {
		t.Errorf("value = %q, want %q", val, "value1")
	}
}

func TestSettingsGetMissing(t *testing.T) {
	db := newTestDB(t)
	ss := NewSettingsStore(db)

	val, err := ss.Get("nonexistent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != "" {
		t.Errorf("value = %q, want empty", val)
	}
}

func TestSettingsUpsert(t *testing.T) {
	db := newTestDB(t)
	ss := NewSettingsStore(db)

	ss.Set("key1", "v1")
	ss.Set("key1", "v2")

	val, _ := ss.Get("key1")
	if val != "v2" {
		t.Errorf("value = %q, want %q", val, "v2")
	}
}

func TestSettingsGetAll(t *testing.T) {
	db := newTestDB(t)
	ss := NewSettingsStore(db)

	ss.Set("a", "1")
	ss.Set("b", "2")

	all, err := ss.GetAll()
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("count = %d, want 2", len(all))
	}
	if all["a"] != "1" || all["b"] != "2" {
		t.Errorf("unexpected values: %v", all)
	}
}

func TestSettingsDelete(t *testing.T) {
	db := newTestDB(t)
	ss := NewSettingsStore(db)

	ss.Set("key1", "val")
	ss.Delete("key1")

	val, _ := ss.Get("key1")
	if val != "" {
		t.Errorf("value should be empty after delete, got %q", val)
	}
}

// --- Server Tests ---

func TestServerCRUD(t *testing.T) {
	db := newTestDB(t)
	ss := NewServerStore(db)

	// Create
	server := &models.Server{
		ID:        "srv-1",
		Name:      "Server 1",
		Hostname:  "host1.example.com",
		IPAddress: "192.168.1.10",
		Status:    "offline",
	}

	if err := ss.Create(server); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Read
	got, err := ss.GetByID("srv-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Server 1" {
		t.Errorf("name = %q, want %q", got.Name, "Server 1")
	}
	if got.RegisteredAt == 0 {
		t.Error("registered_at should be set")
	}

	// Update
	got.Name = "Server 1 Updated"
	got.AgentVersion = "1.0.0"
	if err := ss.Update(got); err != nil {
		t.Fatalf("update: %v", err)
	}

	updated, _ := ss.GetByID("srv-1")
	if updated.Name != "Server 1 Updated" {
		t.Errorf("name = %q, want %q", updated.Name, "Server 1 Updated")
	}
	if updated.AgentVersion != "1.0.0" {
		t.Errorf("version = %q, want %q", updated.AgentVersion, "1.0.0")
	}

	// List
	ss.Create(&models.Server{ID: "srv-2", Name: "Server 2", Hostname: "host2", IPAddress: "10.0.0.2", Status: "online"})
	servers, err := ss.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(servers) != 2 {
		t.Errorf("count = %d, want 2", len(servers))
	}

	// Delete
	if err := ss.Delete("srv-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	servers, _ = ss.List()
	if len(servers) != 1 {
		t.Errorf("count after delete = %d, want 1", len(servers))
	}
}

func TestServerGetByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	ss := NewServerStore(db)

	_, err := ss.GetByID("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestServerUpdateHeartbeat(t *testing.T) {
	db := newTestDB(t)
	ss := NewServerStore(db)

	ss.Create(&models.Server{ID: "srv-1", Name: "S1", Hostname: "h1", IPAddress: "1.2.3.4", Status: "offline"})

	if err := ss.UpdateHeartbeat("srv-1", 1234567890); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	srv, _ := ss.GetByID("srv-1")
	if srv.Status != "online" {
		t.Errorf("status = %q, want %q", srv.Status, "online")
	}
	if srv.LastHeartbeat != 1234567890 {
		t.Errorf("heartbeat = %d, want %d", srv.LastHeartbeat, 1234567890)
	}
}

func TestServerDeleteNotFound(t *testing.T) {
	db := newTestDB(t)
	ss := NewServerStore(db)

	err := ss.Delete("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestServerWithCertificate(t *testing.T) {
	db := newTestDB(t)
	ss := NewServerStore(db)

	cert := []byte("fake-certificate-data")
	ss.Create(&models.Server{ID: "srv-1", Name: "S1", Hostname: "h1", IPAddress: "1.2.3.4", Status: "online", Certificate: cert})

	srv, _ := ss.GetByID("srv-1")
	if string(srv.Certificate) != "fake-certificate-data" {
		t.Errorf("certificate mismatch")
	}
}

func TestServerWithMetadata(t *testing.T) {
	db := newTestDB(t)
	ss := NewServerStore(db)

	ss.Create(&models.Server{
		ID: "srv-1", Name: "S1", Hostname: "h1", IPAddress: "1.2.3.4", Status: "online",
		Metadata: map[string]any{"region": "eu-west", "cpu_cores": float64(8)},
	})

	srv, _ := ss.GetByID("srv-1")
	if srv.Metadata["region"] != "eu-west" {
		t.Errorf("metadata region = %v, want %q", srv.Metadata["region"], "eu-west")
	}
}

// --- Node Tests ---

func TestNodeCRUD(t *testing.T) {
	db := newTestDB(t)
	serverStore := NewServerStore(db)
	nodeStore := NewNodeStore(db)

	// Create server first (foreign key)
	serverStore.Create(&models.Server{ID: "srv-1", Name: "S1", Hostname: "h1", IPAddress: "1.2.3.4", Status: "online"})

	// Create node
	node := &models.Node{
		ID:            "node-1",
		ServerID:      "srv-1",
		Name:          "node1",
		ContainerName: "klever-node1",
		NodeType:      "validator",
		RestAPIPort:   8080,
		DataDirectory: "/opt/node1",
		Status:        "running",
	}

	if err := nodeStore.Create(node); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Read
	got, err := nodeStore.GetByID("node-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "node1" {
		t.Errorf("name = %q, want %q", got.Name, "node1")
	}
	if got.RestAPIPort != 8080 {
		t.Errorf("port = %d, want %d", got.RestAPIPort, 8080)
	}

	// Update
	got.DockerImageTag = "v1.7.16"
	got.DisplayName = "My Validator"
	if err := nodeStore.Update(got); err != nil {
		t.Fatalf("update: %v", err)
	}

	updated, _ := nodeStore.GetByID("node-1")
	if updated.DockerImageTag != "v1.7.16" {
		t.Errorf("tag = %q, want %q", updated.DockerImageTag, "v1.7.16")
	}

	// List by server
	nodeStore.Create(&models.Node{ID: "node-2", ServerID: "srv-1", Name: "node2", ContainerName: "klever-node2", NodeType: "validator", RestAPIPort: 8081, DataDirectory: "/opt/node2", Status: "stopped"})
	nodes, err := nodeStore.ListByServer("srv-1")
	if err != nil {
		t.Fatalf("list by server: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("count = %d, want 2", len(nodes))
	}

	// List all
	all, _ := nodeStore.ListAll("")
	if len(all) != 2 {
		t.Errorf("all count = %d, want 2", len(all))
	}

	// List with filter
	running, _ := nodeStore.ListAll("running")
	if len(running) != 1 {
		t.Errorf("running count = %d, want 1", len(running))
	}

	// Delete
	if err := nodeStore.Delete("node-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	all, _ = nodeStore.ListAll("")
	if len(all) != 1 {
		t.Errorf("count after delete = %d, want 1", len(all))
	}
}

func TestNodeUpdateStatus(t *testing.T) {
	db := newTestDB(t)
	serverStore := NewServerStore(db)
	nodeStore := NewNodeStore(db)

	serverStore.Create(&models.Server{ID: "srv-1", Name: "S1", Hostname: "h1", IPAddress: "1.2.3.4", Status: "online"})
	nodeStore.Create(&models.Node{ID: "node-1", ServerID: "srv-1", Name: "n1", ContainerName: "kn1", NodeType: "validator", RestAPIPort: 8080, DataDirectory: "/opt/n1", Status: "stopped"})

	if err := nodeStore.UpdateStatus("node-1", "running"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	node, _ := nodeStore.GetByID("node-1")
	if node.Status != "running" {
		t.Errorf("status = %q, want %q", node.Status, "running")
	}
}

func TestNodeCascadeDelete(t *testing.T) {
	db := newTestDB(t)
	serverStore := NewServerStore(db)
	nodeStore := NewNodeStore(db)

	serverStore.Create(&models.Server{ID: "srv-1", Name: "S1", Hostname: "h1", IPAddress: "1.2.3.4", Status: "online"})
	nodeStore.Create(&models.Node{ID: "node-1", ServerID: "srv-1", Name: "n1", ContainerName: "kn1", NodeType: "validator", RestAPIPort: 8080, DataDirectory: "/opt/n1", Status: "running"})

	// Deleting server should cascade delete nodes
	serverStore.Delete("srv-1")

	nodes, _ := nodeStore.ListAll("")
	if len(nodes) != 0 {
		t.Errorf("nodes should be cascade deleted, got %d", len(nodes))
	}
}
