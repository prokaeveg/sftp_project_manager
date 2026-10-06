.DEFAULT_GOAL := check

.PHONY: check vet test frontend-test build dev frontend-dev

check: vet test frontend-test

vet:
	go vet ./...

test:
	go test ./... -count=1

frontend-test:
	npm --prefix frontend test

build:
	go run github.com/wailsapp/wails/v2/cmd/wails@v2.16.0 build -platform windows/amd64 -webview2 error

dev:
	go run github.com/wailsapp/wails/v2/cmd/wails@v2.16.0 dev

frontend-dev:
	npm --prefix frontend run dev
