// Package web embeds the dashboard files served by the HTTP server.
package web

import "embed"

//go:embed index.html styles.css app.js
var assets embed.FS

// Files returns the dashboard filesystem.
func Files() embed.FS { return assets }
