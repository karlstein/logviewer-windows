//go:build dev

package main

import "net/http"

func init() {
	staticHandler = http.FileServer(http.Dir("./static"))
}
