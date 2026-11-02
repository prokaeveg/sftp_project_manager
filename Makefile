.DEFAULT_GOAL := check

.PHONY: check vet test frontend-test phpstorm-ui-test comparison-ui-test git-ui-test copy-paths-ui-test build dev frontend-dev

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

# Isolated browser checks; does not read or import user projects.
phpstorm-ui-test:
	npm --prefix frontend run build
	node scripts/verify-phpstorm-ui.mjs

comparison-ui-test:
	npm --prefix frontend run build
	node scripts/verify-file-comparison-ui.mjs

git-ui-test:
	npm --prefix frontend run build
	node scripts/verify-git-ui.mjs

copy-paths-ui-test:
	npm --prefix frontend run build
	node scripts/verify-copy-paths-ui.mjs
