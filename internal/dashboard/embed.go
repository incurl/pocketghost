// Package dashboard embeds the compiled Svelte dashboard so the control plane
// ships as a single static binary.
//
// Build pipeline (Makefile `dashboard` target):
//
//	cd packages/dashboard && pnpm build
//	rsync -a --delete build/ ../pocketghost/internal/dashboard/dist/
//
// When `dist/` only contains the placeholders below, the server serves them and
// the API keeps working; no runtime dependency on Node is introduced.
package dashboard

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the embedded dashboard filesystem rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	return sub
}
