//go:build windows

package main

import (
	"errors"
	"fmt"
	"log"
	"markdown-server/reload"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func StartServingGeneratedFiles() {
	fileSystem := http.FileServer(http.Dir(TargetFolder))

	if os.Getenv("HOT_RELOAD") != "" {
		absolutPath, _ := filepath.Abs(FullPath)

		reloader := reload.New(FullPath)
		reloader.DebugLog = nil
		reloader.OnReload = func(path string, update bool) {
			if update && strings.HasSuffix(path, ".md") {
				fmt.Printf("Regenerated Target of File '%s'\n", path)
				_ = CopyAndTransformMarkdownFile(path, TargetFolder+strings.TrimPrefix(path, absolutPath))
				return
			}
			fmt.Println("Regenerated all Target Files")
			CleanUpFolders()
			WalkFileTreeTwice()
		}
		http.Handle("GET /", reloader.Handle(fileSystem))
	} else {
		http.Handle("GET /", fileSystem)
	}

	server := &http.Server{
		Addr: os.Getenv("ADDRESS"),
	}

	log.Println("Starting server")
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("HTTP server error: %v", err)
	}
}
