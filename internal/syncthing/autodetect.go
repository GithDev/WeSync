package syncthing

import (
	"encoding/xml"
	"fmt"
	"os"
)

type syncthingConfig struct {
	GUI struct {
		APIKey  string `xml:"apikey"`
		Address string `xml:"address"`
		TLS     bool   `xml:"tls,attr"`
	} `xml:"gui"`
}

// AutoDetect reads Syncthing's config.xml from the standard platform location
// and returns the API key and GUI URL. Call this when --syncthing-key is not
// provided on the command line.
func AutoDetect() (apiKey, apiURL string, err error) {
	for _, path := range configCandidates() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg syncthingConfig
		if err := xml.Unmarshal(data, &cfg); err != nil || cfg.GUI.APIKey == "" {
			continue
		}
		addr := cfg.GUI.Address
		if addr == "" {
			addr = "127.0.0.1:8384"
		}
		scheme := "http"
		if cfg.GUI.TLS {
			scheme = "https"
		}
		return cfg.GUI.APIKey, fmt.Sprintf("%s://%s", scheme, addr), nil
	}
	return "", "", fmt.Errorf("Syncthing config not found — make sure Syncthing is running, or pass --syncthing-key manually")
}
