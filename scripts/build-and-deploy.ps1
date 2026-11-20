# PowerShell script to build CPA-Manager-Plus Docker image locally and deploy to server
[CmdletBinding()]
param (
    [switch]$Deploy = $false,
    [string]$ServerHost = "111.229.196.126",
    [string]$ServerKey = "C:\Users\Administrator\.ssh\111.229.196.126_id_ed25519"
)

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent $PSScriptRoot

Write-Host "===> 1. Local Building CPA-Manager-Plus Docker image via local Docker Desktop..." -ForegroundColor Cyan

$Commit = (git -C $RepoRoot rev-parse --short HEAD).Trim()
$Tag = "cpa-manager-plus:deploy-$Commit"

# Local Docker build
docker build `
    -f (Join-Path $RepoRoot "Dockerfile.manager-server") `
    --build-arg VERSION="v1.14.10" `
    --build-arg SOURCE_COMMIT="$Commit" `
    -t $Tag `
    -t "cpa-manager-plus:latest" `
    $RepoRoot

if ($LASTEXITCODE -ne 0) {
    Write-Error "Local Docker build failed!"
    exit 1
}

Write-Host "===> Local Build Success: $Tag" -ForegroundColor Green

if ($Deploy) {
    Write-Host "===> 2. Streaming image to $ServerHost (docker save | ssh docker load)..." -ForegroundColor Cyan
    # Stream compressed image directly to remote Docker daemon without saving local temp files
    docker save $Tag | gzip | ssh -i $ServerKey "root@${ServerHost}" "gunzip | docker load"
    
    Write-Host "===> 3. Updating remote container..." -ForegroundColor Cyan
    ssh -i $ServerKey "root@${ServerHost}" @"
docker stop cpa-manager-plus || true
docker rm cpa-manager-plus || true
docker tag $Tag cpa-manager-plus:current
docker run -d --name cpa-manager-plus --restart always --network host \
  -v /data/cpa-manager-plus:/data \
  cpa-manager-plus:current
docker image prune -f
"@
    Write-Host "===> CPA-Manager-Plus Deployment Completed Successfully!" -ForegroundColor Green
} else {
    Write-Host "Build complete. Pass -Deploy switch to automatically stream image to server and run." -ForegroundColor Yellow
}
