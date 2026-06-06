$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
Set-Location $repoRoot

$env:GOCACHE = Join-Path $repoRoot ".gocache"
go run ./tools/build @args
