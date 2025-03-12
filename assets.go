package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Function called in main to ensure that /assets dir exist or if not, create it
func (cfg apiConfig) ensureAssetsDir() error {
	if _, err := os.Stat(cfg.assetsRoot); os.IsNotExist(err) {
		return os.Mkdir(cfg.assetsRoot, 0755)
	}
	return nil
}

// Get a proper file path but with a random base64 string id
func getAssetPath(mediaType string) string {
	ext := mediaTypeToExt(mediaType)
	// Create a 32-byte slice - Fill it with random data - Convert to bas54 URL-safe string
	randBytes := make([]byte, 32)
	rand.Read(randBytes)
	randString := base64.RawURLEncoding.EncodeToString(randBytes)
	return fmt.Sprintf("%s%s", randString, ext)
}

// Get a proper assets/<asset_path> path
func (cfg *apiConfig) getAssetDiskPath(assetPath string) string {
	return filepath.Join(cfg.assetsRoot, assetPath)
}

// Get a proper asset URL
func (cfg *apiConfig) getAssetURL(assetPath string) string {
	return fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, assetPath)
}

func mediaTypeToExt(mediaType string) string {
	parts := strings.Split(mediaType, "/")
	if len(parts) != 2 {
		return ".bin"
	}
	return "." + parts[1]
}
