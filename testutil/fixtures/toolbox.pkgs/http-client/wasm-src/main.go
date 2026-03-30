package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) == 0 {
		log.Fatalf("usage: http-client <url>")
	}

	targetURL := os.Args[len(os.Args)-1]
	if targetURL == "" {
		log.Fatalf("usage: http-client <url>")
	}

	resp, err := http.Get(targetURL)
	if err != nil {
		log.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Read failed: %v", err)
	}

	fmt.Printf("Status: %s\nBody:\n%s\n", resp.Status, string(body))
}
