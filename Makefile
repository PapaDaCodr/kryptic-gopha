.PHONY: run stop restart build clean help

# --- Variables ---
BINARY_NAME=kryptic-gopha
PORT=8080

# --- Commands ---

## run: Kill existing process and run the server
run: stop
	@echo "Starting $(BINARY_NAME)..."
	@go run cmd/server/main.go > server.log 2>&1 &
	@echo "Server is running in background. Port: $(PORT)"
	@echo "Logs are being written to server.log"

## stop: Safely terminate the running bot
stop:
	@echo "Stopping $(BINARY_NAME) on port $(PORT)..."
	@-fuser -k $(PORT)/tcp > /dev/null 2>&1 || true
	@echo "Cleanup complete."

## restart: Restart the engine
restart: stop run

## build: Compile the binary
build:
	@echo " Building $(BINARY_NAME)..."
	@go build -o $(BINARY_NAME) cmd/server/main.go

## clean: Remove binary and logs
clean: stop
	@echo " Cleaning up..."
	@rm -f $(BINARY_NAME)
	@rm -f server.log
	@echo "✅ Done."

## help: Show this help message
help:
	@echo "Kryptic Gopha Management Hub"
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^##' Makefile | sed 's/## //' | column -t -s ':'
