//go:build !windows

package main

import (
	"errors"
	"log"
	"net/http"
	"os"
)

func StartServingGeneratedFiles() {
	fileSystem := http.FileServer(http.Dir(TargetFolder))

	http.Handle("GET /", fileSystem)

	server := &http.Server{
		Addr: os.Getenv("ADDRESS"),
	}

	log.Println("Starting server")
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("HTTP server error: %v", err)
	}
}
