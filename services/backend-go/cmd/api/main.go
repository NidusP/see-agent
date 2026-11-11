package main

import (
	"log"
	"os"

	"see-agent/services/backend-go/internal/server"
)

func main() {
	addr := os.Getenv("BACKEND_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	srv := server.New(addr)
	for _, item := range srv.StartupSummary() {
		log.Printf("backend startup %s", item)
	}
	log.Printf("backend listening on %s", addr)

	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
