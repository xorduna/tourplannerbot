package webapp

import (
	"embed"
	"fmt"
	"io/fs"
)

// embeddedAssets contains the Vite production build. The frontend build must
// run before compiling this package so that dist exists for go:embed.
//
//go:embed dist
var embeddedAssets embed.FS

// embeddedAssetFileSystem returns the Vite output directory as a filesystem
// whose root contains index.html and the versioned assets directory.
func embeddedAssetFileSystem() fs.FS {
	assetFileSystem, err := fs.Sub(embeddedAssets, "dist")
	if err != nil {
		panic(fmt.Sprintf("open embedded Mini App assets: %v", err))
	}
	return assetFileSystem
}
