param([switch]$DebugBuild)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$profileName = if ($DebugBuild) { 'debug' } else { 'release' }
Push-Location (Join-Path $projectRoot 'frontend')
try {
    & pnpm install --frozen-lockfile
    if ($LASTEXITCODE -ne 0) { throw 'Frontend dependency installation failed' }
    & pnpm build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
} finally { Pop-Location }
Push-Location $projectRoot
$previousRustFlags = $env:RUSTFLAGS
if ($env:RUSTFLAGS -notmatch '\+crt-static') {
    $env:RUSTFLAGS = "$previousRustFlags -C target-feature=+crt-static".Trim()
}
try {
    $buildArgs = @('build', '--manifest-path', 'rust/Cargo.toml', '--locked', '--workspace')
    if (-not $DebugBuild) { $buildArgs += '--release' }
    & cargo @buildArgs
    if ($LASTEXITCODE -ne 0) { throw 'Rust build failed' }
    $targetRoot = if ($env:CARGO_TARGET_DIR) { $env:CARGO_TARGET_DIR } else { Join-Path $projectRoot 'rust/target' }
    $outputPath = Join-Path $projectRoot 'build/r-tam/windows'
    New-Item -ItemType Directory -Force -Path $outputPath | Out-Null
    foreach ($binaryName in @('r-tam-server.exe', 'r-tam-client.exe')) {
        Copy-Item -LiteralPath (Join-Path $targetRoot "$profileName/$binaryName") -Destination $outputPath -Force
    }
    Write-Output "Built R-TAM programs in $outputPath"
} finally {
    $env:RUSTFLAGS = $previousRustFlags
    Pop-Location
}
