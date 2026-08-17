package secrets_test

import (
	"os"
	"testing"

	"gateway/internal/secrets"
)

func TestSecretsEncryptDecrypt(t *testing.T) {
	// Create temporary directory for secrets
	tmpDir := t.TempDir()

	// Create secrets manager (use stub for testing)
	mgr, err := secrets.NewManager(tmpDir, true)
	if err != nil {
		t.Fatalf("Failed to create secrets manager: %v", err)
	}

	// Test encrypt/decrypt cycle
	plaintext := "my-secret-api-key-12345"

	keyHash, keyRef, err := mgr.StoreSecret(plaintext)
	if err != nil {
		t.Fatalf("Failed to store secret: %v", err)
	}

	if keyHash == "" {
		t.Error("keyHash should not be empty")
	}
	if keyRef == "" {
		t.Error("keyRef should not be empty")
	}

	// Retrieve and verify
	retrieved, err := mgr.RetrieveSecret(keyRef)
	if err != nil {
		t.Fatalf("Failed to retrieve secret: %v", err)
	}

	if retrieved != plaintext {
		t.Errorf("Retrieved secret does not match. Expected %s, got %s", plaintext, retrieved)
	}

	// Verify hash
	if !mgr.VerifySecret(plaintext, keyHash) {
		t.Error("VerifySecret failed for correct plaintext")
	}

	if mgr.VerifySecret("wrong-key", keyHash) {
		t.Error("VerifySecret should fail for wrong plaintext")
	}
}

func TestSecretsNoPlaintext(t *testing.T) {
	// Create temporary directory for secrets
	tmpDir := t.TempDir()

	// Create secrets manager
	mgr, err := secrets.NewManager(tmpDir, true)
	if err != nil {
		t.Fatalf("Failed to create secrets manager: %v", err)
	}

	plaintext := "my-secret-key"
	_, keyRef, err := mgr.StoreSecret(plaintext)
	if err != nil {
		t.Fatalf("Failed to store secret: %v", err)
	}

	// Read the stored file
	secretFile := tmpDir + "/" + keyRef
	content, err := os.ReadFile(secretFile)
	if err != nil {
		t.Fatalf("Failed to read secret file: %v", err)
	}

	// Verify plaintext is not in the file
	if string(content) == plaintext {
		t.Error("Plaintext found in secret file - should be encrypted!")
	}
}

func TestSecretsEnvironmentOverride(t *testing.T) {
	// Create temporary directory for secrets
	tmpDir := t.TempDir()

	// Create secrets manager
	mgr, err := secrets.NewManager(tmpDir, true)
	if err != nil {
		t.Fatalf("Failed to create secrets manager: %v", err)
	}

	plaintext := "original-secret"
	_, keyRef, err := mgr.StoreSecret(plaintext)
	if err != nil {
		t.Fatalf("Failed to store secret: %v", err)
	}

	// Set environment override
	envKey := "SECRET_" + keyRef
	envValue := "override-from-env"
	os.Setenv(envKey, envValue)
	defer os.Unsetenv(envKey)

	// Retrieve - should get env value
	retrieved, err := mgr.RetrieveSecret(keyRef)
	if err != nil {
		t.Fatalf("Failed to retrieve secret: %v", err)
	}

	if retrieved != envValue {
		t.Errorf("Expected environment override %s, got %s", envValue, retrieved)
	}
}
