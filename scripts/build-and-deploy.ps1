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
    Write-Host "===> 2. Saving and deploying image to $ServerHost..." -ForegroundColor Cyan
    $LocalTar = Join-Path ([System.IO.Path]::GetTempPath()) "cpamp-$Commit.tar"
    $RemoteTar = "/tmp/cpamp-$Commit.tar"

    try {
        Write-Host "Saving local image $Tag to temp tar..." -ForegroundColor Cyan
        docker save $Tag -o $LocalTar

        Write-Host "Transferring to $ServerHost via scp..." -ForegroundColor Cyan
        scp -i $ServerKey $LocalTar "root@${ServerHost}:${RemoteTar}"

        Write-Host "Loading image and updating container via compose..." -ForegroundColor Cyan
        $DeployImage = "cpa-manager-plus:controlplane-v1.14.10-$Commit"
        ssh -i $ServerKey "root@${ServerHost}" @"
docker load -i $RemoteTar
rm -f $RemoteTar
docker tag $Tag $DeployImage
sed -i -E 's|image: cpa-manager-plus:.*|image: $DeployImage|' /opt/cpa-manager-plus/compose.yaml
cd /opt/cpa-manager-plus && docker compose up -d
docker image prune -f
"@
    } finally {
        if (Test-Path $LocalTar) {
            Remove-Item $LocalTar -Force -ErrorAction SilentlyContinue
        }
    }
    Write-Host "===> CPA-Manager-Plus Deployment Completed Successfully!" -ForegroundColor Green
} else {
    Write-Host "Build complete. Pass -Deploy switch to automatically stream image to server and run." -ForegroundColor Yellow
}
