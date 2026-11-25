$ErrorActionPreference = 'Stop'
$modules = Get-ChildItem -Recurse -Filter go.mod | ForEach-Object { $_.Directory.FullName }
foreach ($mod in $modules) {
  Write-Host "=== Testing module $mod ==="
  Set-Location $mod
  Write-Host "Running go test ./..."
  go test -count=1 ./...
  Write-Host "Running go test -race ./..."
  go test -race -count=1 ./...
  Write-Host "Running go vet ./..."
  go vet ./...
}
