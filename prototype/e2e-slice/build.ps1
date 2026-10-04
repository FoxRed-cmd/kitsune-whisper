# THROWAWAY prototype build. Requires Go and zig on PATH.
# malgo needs cgo -> use zig's bundled clang as the C compiler.
$env:CGO_ENABLED = "1"
if (-not $env:CC) { $env:CC = "zig cc" }
Write-Host "CC=$env:CC  CGO_ENABLED=$env:CGO_ENABLED"
Push-Location (Join-Path $PSScriptRoot "client")
try {
    go build -o ..\kitsune-prototype.exe .
} finally {
    Pop-Location
}
