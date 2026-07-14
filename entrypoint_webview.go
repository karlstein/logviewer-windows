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

	// Bridge console.log from the webview to Go's log so output
	// appears in the terminal instead of only in the webview inspector.
	w.Bind("goLog", func(msg string) {
		log.Printf("JS: %s", msg)
	})
	w.Init(`
console.log = (function(orig) {
  return function() {
    var args = Array.prototype.slice.call(arguments);
    var msg = args.map(function(a) {
      if (typeof a === 'object') { try { return JSON.stringify(a); } catch(e) {} }
      return String(a);
    }).join(' ');
    try { goLog(msg); } catch(e) {}
    orig.apply(console, args);
  };
})(console.log);
`)

	w.Navigate(fmt.Sprintf("http://127.0.0.1:%d", port))
	w.Run()
	w.Destroy()

	cleanup(httpServer, srv)
}
