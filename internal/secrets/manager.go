package secrets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Manager handles secret encryption and decryption
type Manager struct {
	secretsDir string
	backend    Backend
}

// Backend defines the interface for secret storage backends
type Backend interface {
	Encrypt(plaintext []byte) (ciphertext []byte, err error)
	Decrypt(ciphertext []byte) (plaintext []byte, err error)
}

// NewManager creates a new secrets manager
func NewManager(secretsDir string, useStub bool) (*Manager, error) {
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create secrets directory: %w", err)
	}

	var backend Backend
	var err error

	if useStub {
		backend, err = newStubBackend()
	} else {
		backend, err = newWindowsBackend()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to initialize secrets backend: %w", err)
	}

	return &Manager{
		secretsDir: secretsDir,
		backend:    backend,
	}, nil
}

// StoreSecret encrypts and stores a secret, returning the key_hash and key_ref
func (m *Manager) StoreSecret(plaintext string) (keyHash, keyRef string, err error) {
	// Generate key_hash
	hash := sha256.Sum256([]byte(plaintext))
	keyHash = hex.EncodeToString(hash[:])

	// Generate unique key_ref
	randBytes := make([]byte, 16)
	if _, err := rand.Read(randBytes); err != nil {
		return "", "", fmt.Errorf("failed to generate key_ref: %w", err)
	}
	keyRef = hex.EncodeToString(randBytes)

	// Encrypt
	ciphertext, err := m.backend.Encrypt([]byte(plaintext))
	if err != nil {
		return "", "", fmt.Errorf("failed to encrypt secret: %w", err)
	}

	// Store to file
	secretPath := filepath.Join(m.secretsDir, keyRef)
	if err := os.WriteFile(secretPath, ciphertext, 0600); err != nil {
		return "", "", fmt.Errorf("failed to write secret file: %w", err)
	}

	return keyHash, keyRef, nil
}

// RetrieveSecret retrieves and decrypts a secret by key_ref
func (m *Manager) RetrieveSecret(keyRef string) (string, error) {
	// Check environment variable override first
	if envVal := os.Getenv("SECRET_" + keyRef); envVal != "" {
		return envVal, nil
	}

	// Read from file
	secretPath := filepath.Join(m.secretsDir, keyRef)
	ciphertext, err := os.ReadFile(secretPath)
	if err != nil {
		return "", fmt.Errorf("failed to read secret file: %w", err)
	}

	// Decrypt
	plaintext, err := m.backend.Decrypt(ciphertext)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt secret: %w", err)
	}

	return string(plaintext), nil
}

// VerifySecret checks if a plaintext matches a key_hash
func (m *Manager) VerifySecret(plaintext, keyHash string) bool {
	hash := sha256.Sum256([]byte(plaintext))
	computed := hex.EncodeToString(hash[:])
	return computed == keyHash
}

// HashSecret computes the SHA256 hash of a plaintext secret
func HashSecret(plaintext string) string {
	hash := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(hash[:])
}
