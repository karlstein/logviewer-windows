//go:build serveronly

package main

import (
	"fmt"
	"os"
	"os/signal"
)

func main() {
	port, httpServer, srv := runServer()
	fmt.Printf("╔══════════════════════════════════════════════╗\n")
	fmt.Printf("║  Log Viewer                                  ║\n")
	fmt.Printf("║                                              ║\n")
	fmt.Printf("║  Open in your browser:                       ║\n")
	fmt.Printf("║  →  http://127.0.0.1:%-5d                    ║\n", port)
	fmt.Printf("║                                              ║\n")
	fmt.Printf("║  Press Ctrl+C to stop                        ║\n")
	fmt.Printf("╚══════════════════════════════════════════════╝\n")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig

	fmt.Println("\nShutting down...")
	cleanup(httpServer, srv)
}
