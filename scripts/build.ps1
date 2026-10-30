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
        (Join-Path $projectRoot 'README.md')
    )
    Compress-Archive -LiteralPath $releaseFiles -DestinationPath (Join-Path $projectRoot 'dist/sftp-project-manager-windows-amd64.zip') -Force
} finally { Pop-Location }
