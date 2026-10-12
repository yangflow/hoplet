package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yangflow/hoplet/agent/internal/config"
)

func ensureDeviceID(cfg *config.Config) (string, error) {
	if cfg.DeviceID != "" {
		return cfg.DeviceID, nil
	}
	path := filepath.Join(cfg.HopletHome, "device-id")
	if raw, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(raw))
		if len(id) >= 8 {
			return id, nil
		}
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	if err := os.MkdirAll(cfg.HopletHome, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		return "", fmt.Errorf("write device-id: %w", err)
	}
	return id, nil
}
