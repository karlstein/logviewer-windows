# Log Viewer — Build, run, and maintenance targets
#
# Native window (default): requires WebView2 (Windows/macOS) or
#   libwebkit2gtk-4.0-dev + pkg-config (Linux).
# Server-only mode:     make build-server
#   No GUI, just prints a URL to open in your browser.

BINARY    := logviewer
BINARY_WIN:= logviewer.exe

SRC       := .

# Detect host OS
ifeq ($(OS),Windows_NT)
HOST_OS := windows
else
HOST_OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
endif

.PHONY: all build build-windows build-server build-all clean run run-server test

# ------------------------------------------------------------------
# Default target: native build with WebView window
# ------------------------------------------------------------------
all: build

# ------------------------------------------------------------------
# build          — Native build for current OS (opens a WebView window)
# build-windows  — Build for Windows (native or cross-compile)
# build-server   — Server-only mode (prints URL, no GUI deps needed)
# build-all      — Build native + server-only for current OS
# ------------------------------------------------------------------
build:
	@echo "Building $(BINARY) with native WebView window..."
	go build -o $(BINARY) $(SRC)
	@echo "Done: $(BINARY)"
	@if [ "$(HOST_OS)" = "linux" ]; then echo "Note: requires libwebkit2gtk-4.0-dev + pkg-config"; fi

build-windows:
ifeq ($(HOST_OS),windows)
	@echo "Building $(BINARY_WIN) for Windows (native)..."
	CGO_ENABLED=1 go build -o $(BINARY_WIN) $(SRC)
	@echo "Done: $(BINARY_WIN)"
else
	@echo "Building $(BINARY_WIN) for Windows (cross-compile)..."
	@echo "(requires x86_64-w64-mingw32-gcc and CGO_ENABLED=1)"
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 \
		CC=x86_64-w64-mingw32-gcc \
		CXX=x86_64-w64-mingw32-g++ \
		go build -o $(BINARY_WIN) $(SRC)
	@echo "Done: $(BINARY_WIN)"
endif

build-server:
	@echo "Building $(BINARY) in server-only mode (no WebView)..."
	go build -tags serveronly -o $(BINARY) $(SRC)
	@echo "Done: $(BINARY) (server-only)"

build-all: build build-server

# ------------------------------------------------------------------
# clean — Remove all built binaries
# ------------------------------------------------------------------
clean:
	@rm -f $(BINARY) $(BINARY_WIN)
	@echo "Cleaned."

# ------------------------------------------------------------------
# run        — Build with WebView then start
# run-server — Build server-only then start
# ------------------------------------------------------------------
run: build
	@echo "Starting Log Viewer..."
	./$(BINARY)

run-server: build-server
	@echo "Starting Log Viewer in server-only mode..."
	./$(BINARY)

# ------------------------------------------------------------------
# test — Run all package tests
# ------------------------------------------------------------------
test:
	@go test ./...
