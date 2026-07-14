//go:build !serveronly

package main

import (
	"fmt"
	"log"

	"github.com/webview/webview_go"
)

func main() {
	port, httpServer, srv := runServer()
	log.Printf("Log Viewer ready — opening native window...")

	w := webview.New(true)
	w.SetTitle("Log Viewer")
	w.SetSize(1200, 800, webview.HintNone)
	w.Navigate(fmt.Sprintf("http://127.0.0.1:%d", port))
	w.Run()
	w.Destroy()

	cleanup(httpServer, srv)
}
