[CmdletBinding()]
param(
    [switch]$Race,
    [switch]$Security
)

$ErrorActionPreference = 'Stop'
Push-Location -LiteralPath $PSScriptRoot
try {
    # Runtime data can contain local Go diagnostic scripts. Check only the
    # application's packages, without modifying or executing those scripts.
    $goTestArgs = @('test')
    if ($Race) { $goTestArgs += '-race' }
    $goTestArgs += './cmd/...', './internal/...'
    & go @goTestArgs
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    & go vet ./cmd/... ./internal/...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
    & npm.cmd test --prefix web
    if ($LASTEXITCODE -ne 0) { throw 'Frontend tests failed' }
    # A running server reads web/dist directly. Verification must not replace it.
    & npm.cmd run build --prefix web -- --outDir ../build/review-web
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
    if ($Security) {
        & npm.cmd audit --prefix web
        if ($LASTEXITCODE -ne 0) { throw 'Frontend dependency audit failed' }
        & go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./cmd/... ./internal/...
        if ($LASTEXITCODE -ne 0) { throw 'Go vulnerability scan failed' }
    }
} finally {
    Pop-Location
}
