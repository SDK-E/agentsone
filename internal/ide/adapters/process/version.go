// SPDX-License-Identifier: MIT

package process

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DefaultDir is where the image unpacks the VSCodium server.
const DefaultDir = "/opt/vscodium-server"

// Version is the VS Code version of the server in dir, from its
// package.json, or "unknown" when there is no readable one.
func Version(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "package.json")) // #nosec G304 -- fixed install folder
	if err != nil {
		return "unknown"
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &pkg) != nil || pkg.Version == "" {
		return "unknown"
	}
	return pkg.Version
}
