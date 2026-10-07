$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
try {
    go run github.com/wailsapp/wails/v2/cmd/wails@v2.16.0 build -platform windows/amd64 -webview2 error
    if ($LASTEXITCODE -ne 0) { throw 'Windows build failed' }
    New-Item -ItemType Directory -Force -Path (Join-Path $projectRoot 'dist') | Out-Null
    # Package only explicit release files; never include user data from a previous launch.
    $releaseFiles = @(
        (Join-Path $projectRoot 'build/bin/sftp-project-manager.exe'),
        (Join-Path $projectRoot 'docs/smoke-test.md'),
        (Join-Path $projectRoot 'docs/verification.md'),
        (Join-Path $projectRoot 'docs/proxy-connections.md'),
        (Join-Path $projectRoot 'docs/exclusions.md'),
        (Join-Path $projectRoot 'docs/file-trees.md'),
        (Join-Path $projectRoot 'docs/remote-navigation.md'),
        (Join-Path $projectRoot 'docs/root-deployment.md'),
        (Join-Path $projectRoot 'docs/password-auth.md'),
        (Join-Path $projectRoot 'docs/batch-transfers.md'),
        (Join-Path $projectRoot 'docs/transfer-queue.md'),
        (Join-Path $projectRoot 'docs/ide-workspace.md'),
        (Join-Path $projectRoot 'README.md')
    )
    Compress-Archive -LiteralPath $releaseFiles -DestinationPath (Join-Path $projectRoot 'dist/sftp-project-manager-windows-amd64.zip') -Force
} finally { Pop-Location }
