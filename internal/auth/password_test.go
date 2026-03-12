package auth

import (
	"testing"
)

func TestSetAndVerifyPassword(t *testing.T) {
	pm := NewPasswordManager("")

	hash, err := pm.SetPassword("mysecurepassword")
	if err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}

	if !pm.Verify("mysecurepassword") {
		t.Error("expected password to verify")
	}
	if pm.Verify("wrongpassword") {
		t.Error("expected wrong password to fail")
	}
}

func TestPasswordMinLength(t *testing.T) {
	pm := NewPasswordManager("")

	_, err := pm.SetPassword("short")
	if err != ErrPasswordTooShort {
		t.Errorf("expected ErrPasswordTooShort, got %v", err)
	}

	_, err = pm.SetPassword("12345678")
	if err != nil {
		t.Errorf("expected 8-char password to succeed, got %v", err)
	}
}

func TestHasPassword(t *testing.T) {
	pm := NewPasswordManager("")
	if pm.HasPassword() {
		t.Error("expected no password initially")
	}

	if _, err := pm.SetPassword("testpassword"); err != nil {
		t.Fatal(err)
	}
	if !pm.HasPassword() {
		t.Error("expected password after set")
	}
}

func TestPasswordFromStored(t *testing.T) {
	pm1 := NewPasswordManager("")
	hash, err := pm1.SetPassword("persistedpassword")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate loading from store
	pm2 := NewPasswordManager(hash)
	if !pm2.HasPassword() {
		t.Error("expected password from stored hash")
	}
	if !pm2.Verify("persistedpassword") {
		t.Error("expected stored password to verify")
	}
	if pm2.Verify("differentpassword") {
		t.Error("expected wrong password to fail")
	}
}

func TestChangePassword(t *testing.T) {
	pm := NewPasswordManager("")

	if _, err := pm.SetPassword("oldpassword1"); err != nil {
		t.Fatal(err)
	}
	if !pm.Verify("oldpassword1") {
		t.Error("old password should verify")
	}

	if _, err := pm.SetPassword("newpassword2"); err != nil {
		t.Fatal(err)
	}
	if pm.Verify("oldpassword1") {
		t.Error("old password should no longer verify")
	}
	if !pm.Verify("newpassword2") {
		t.Error("new password should verify")
	}
}
