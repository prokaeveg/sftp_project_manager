$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
try {
    $goEnv = @{}
    foreach ($name in @('GOOS', 'GOARCH', 'CGO_ENABLED')) {
        $goEnv[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
    }
    $windowsResource = Join-Path $projectRoot 'wails_windows_amd64.syso'
    $generatedWindowsResource = $false
    try {
        # The application embeds frontend/dist, which is absent in a clean checkout
        # until the frontend build below. Keep a temporary file for Go package loading.
        $frontendDist = Join-Path $projectRoot 'frontend/dist'
        $bindingPlaceholder = Join-Path $frontendDist '.bindings-generation-placeholder'
        New-Item -ItemType Directory -Force -Path $frontendDist | Out-Null
        Set-Content -LiteralPath $bindingPlaceholder -Value 'temporary' -Encoding UTF8

        # Generate TypeScript bindings with the pinned v3 CLI; no global install is needed.
        go run github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0 generate bindings -ts -i -d frontend/bindings ./...
        if ($LASTEXITCODE -ne 0) { throw 'Wails v3 bindings generation failed' }
        Remove-Item -LiteralPath $bindingPlaceholder -Force

        npm --prefix frontend ci
        if ($LASTEXITCODE -ne 0) { throw 'Frontend dependency installation failed' }

        npm --prefix frontend run build
        if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }

        $outputDirectory = Join-Path $projectRoot 'build/bin'
        New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
        $executable = Join-Path $outputDirectory 'sftp-project-manager.exe'
        if (Test-Path -LiteralPath $executable) { Remove-Item -LiteralPath $executable -Force }

        $env:GOOS = 'windows'
        $env:GOARCH = 'amd64'
        $env:CGO_ENABLED = '0'
        if (Test-Path -LiteralPath $windowsResource) {
            throw "Refusing to overwrite an existing Windows resource file: $windowsResource"
        }
        go run github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0 generate syso -arch amd64 -icon build/windows/icon.ico -manifest build/windows/wails.exe.manifest -info build/windows/info.json -out wails_windows_amd64.syso
        $generatedWindowsResource = Test-Path -LiteralPath $windowsResource
        if ($LASTEXITCODE -ne 0) { throw 'Windows icon and manifest generation failed' }

        go build -tags production -trimpath -buildvcs=false -ldflags '-w -s -H windowsgui' -o $executable .
        if ($LASTEXITCODE -ne 0) { throw 'Windows amd64 Go build failed' }

        $distDirectory = Join-Path $projectRoot 'dist'
        New-Item -ItemType Directory -Force -Path $distDirectory | Out-Null
        $archive = Join-Path $distDirectory 'sftp-project-manager-windows-amd64.zip'
        if (Test-Path -LiteralPath $archive) { Remove-Item -LiteralPath $archive -Force }
        Compress-Archive -LiteralPath @($executable, (Join-Path $projectRoot 'README.md')) -DestinationPath $archive -Force
    } finally {
        foreach ($name in @('GOOS', 'GOARCH', 'CGO_ENABLED')) {
            [Environment]::SetEnvironmentVariable($name, $goEnv[$name], 'Process')
        }
        if ($bindingPlaceholder -and (Test-Path -LiteralPath $bindingPlaceholder)) {
            Remove-Item -LiteralPath $bindingPlaceholder -Force
        }
        if ($generatedWindowsResource -and (Test-Path -LiteralPath $windowsResource)) {
            Remove-Item -LiteralPath $windowsResource -Force
        }
    }
} finally { Pop-Location }
