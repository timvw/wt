#!/usr/bin/env pwsh
# Local Windows e2e test script
# Run this in PowerShell on Windows or PowerShell Core on any OS

param(
    [switch]$Verbose
)

$ErrorActionPreference = 'Stop'

Write-Host "==> Building wt binary..." -ForegroundColor Cyan
go build -o wt.exe .
if ($LASTEXITCODE -ne 0) {
    Write-Error "Build failed"
    exit 1
}

Write-Host "==> Setting up test environment..." -ForegroundColor Cyan
$WORK_DIR = Get-Location
$WT_BIN = Join-Path $WORK_DIR "wt.exe"
$TEST_ROOT = Join-Path $env:TEMP ("wt-test-" + (Get-Random))
$WORKTREE_ROOT = Join-Path $TEST_ROOT "worktrees"
$TEST_REPO = Join-Path $TEST_ROOT "test-repo"

Write-Host "    Test root: $TEST_ROOT" -ForegroundColor Gray
Write-Host "    Worktree root: $WORKTREE_ROOT" -ForegroundColor Gray
Write-Host "    Test repo: $TEST_REPO" -ForegroundColor Gray

try {
    New-Item -ItemType Directory -Path $TEST_REPO | Out-Null

    Write-Host "==> Initializing git repo..." -ForegroundColor Cyan
    Set-Location $TEST_REPO
    git init
    git config user.email "test@example.com"
    git config user.name "Test User"
    git commit --allow-empty -m "initial commit"
    git branch -M main

    Write-Host "==> Creating test branch..." -ForegroundColor Cyan
    git checkout -b test-branch
    git commit --allow-empty -m "test commit"
    git checkout main

    Write-Host "==> Loading shellenv..." -ForegroundColor Cyan
    $env:WORKTREE_ROOT = $WORKTREE_ROOT

    # Get shellenv output
    $shellenvOutput = & $WT_BIN shellenv
    if ($Verbose) {
        Write-Host "--- Shellenv output ---" -ForegroundColor Yellow
        $shellenvOutput | ForEach-Object { Write-Host $_ -ForegroundColor Gray }
        Write-Host "--- End shellenv ---" -ForegroundColor Yellow
    }

    # Load it
    $shellenvString = $shellenvOutput -join "`n"
    Invoke-Expression $shellenvString

    # Verify wt function is defined
    if (!(Get-Command wt -ErrorAction SilentlyContinue)) {
        Write-Error "wt function not defined after loading shellenv"
        exit 1
    }
    Write-Host "    ✓ wt function loaded" -ForegroundColor Green

    Write-Host "==> Testing: wt checkout test-branch" -ForegroundColor Cyan
    $checkoutOutput = wt checkout test-branch
    if ($Verbose) {
        Write-Host "    Output: $checkoutOutput" -ForegroundColor Gray
    }

    $currentDir = (Get-Location).Path
    $expectedDir = Join-Path $WORKTREE_ROOT "test-repo\test-branch"

    Write-Host "    Current dir: $currentDir" -ForegroundColor Gray
    Write-Host "    Expected dir: $expectedDir" -ForegroundColor Gray

    if ($currentDir -ne $expectedDir) {
        Write-Error "❌ Auto-cd failed after checkout!"
        Write-Host "    Expected: $expectedDir" -ForegroundColor Red
        Write-Host "    Got: $currentDir" -ForegroundColor Red
        exit 1
    }
    Write-Host "    ✓ Auto-cd worked for checkout" -ForegroundColor Green

    Write-Host "==> Testing: wt list" -ForegroundColor Cyan
    $listOutput = wt list
    if ($listOutput -notmatch 'test-branch') {
        Write-Error "❌ Worktree not in list"
        exit 1
    }
    Write-Host "    ✓ Worktree listed" -ForegroundColor Green

    Write-Host "==> Going back to test repo..." -ForegroundColor Cyan
    Set-Location $TEST_REPO

    Write-Host "==> Testing: wt create feature-test" -ForegroundColor Cyan
    $createOutput = wt create feature-test
    if ($Verbose) {
        Write-Host "    Output: $createOutput" -ForegroundColor Gray
    }

    $currentDir = (Get-Location).Path
    $expectedDir = Join-Path $WORKTREE_ROOT "test-repo\feature-test"

    Write-Host "    Current dir: $currentDir" -ForegroundColor Gray
    Write-Host "    Expected dir: $expectedDir" -ForegroundColor Gray

    if ($currentDir -ne $expectedDir) {
        Write-Error "❌ Auto-cd failed after create!"
        Write-Host "    Expected: $expectedDir" -ForegroundColor Red
        Write-Host "    Got: $currentDir" -ForegroundColor Red
        exit 1
    }
    Write-Host "    ✓ Auto-cd worked for create" -ForegroundColor Green

    Write-Host ""
    Write-Host "✅ All tests passed!" -ForegroundColor Green

} finally {
    Set-Location $WORK_DIR
    if (Test-Path $TEST_ROOT) {
        Remove-Item -Recurse -Force $TEST_ROOT -ErrorAction SilentlyContinue
        Write-Host "==> Cleaned up test directory" -ForegroundColor Gray
    }
}
