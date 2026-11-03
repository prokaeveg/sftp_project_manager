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
	powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\build.ps1

dev:
	powershell.exe -NoProfile -Command "Set-Item Env:FRONTEND_DEVSERVER_URL 'http://localhost:5173'; go run ."

frontend-dev:
	npm --prefix frontend run dev

# Isolated browser checks; does not read or import user projects.
phpstorm-ui-test:
	npm --prefix frontend run build:ui-test
	node scripts/verify/verify-phpstorm-ui.mjs

comparison-ui-test:
	npm --prefix frontend run build:ui-test
	node scripts/verify/verify-file-comparison-ui.mjs

git-ui-test:
	npm --prefix frontend run build:ui-test
	node scripts/verify/verify-git-ui.mjs

copy-paths-ui-test:
	npm --prefix frontend run build:ui-test
	node scripts/verify/verify-copy-paths-ui.mjs
