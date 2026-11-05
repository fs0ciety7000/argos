// Package web exposes the compiled Svelte app (web/dist) to the Go server.
package web

import "embed"

// Dist holds the Vite build output. Run `npm run build` before `go build`;
// dist/.gitkeep keeps the embed valid when the frontend is not built yet.
//
//go:embed all:dist
var Dist embed.FS
