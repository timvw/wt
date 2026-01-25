# Shared setup functions for e2e tests (PowerShell)
# Dot-sourced by the PowerShell runner

$ErrorActionPreference = "Stop"

# Global test variables
$script:TestDir = $null
$script:RepoDir = $null
$script:RepoName = "test-repo"
$script:WorktreeRoot = $null
$script:WtBin = $null

# Initialize test environment
function Initialize-E2ETest {
    param(
        [Parameter(Mandatory=$true)]
        [string]$WtBinary
    )

    if (-not (Test-Path $WtBinary)) {
        Write-Host "ERROR: wt binary not found: $WtBinary" -ForegroundColor Red
        exit 1
    }

    $script:WtBin = $WtBinary
    $script:TestDir = Join-Path $env:TEMP "wt-e2e-$(Get-Random)"
    $script:RepoDir = Join-Path $script:TestDir "test-repo"
    $script:WorktreeRoot = Join-Path $script:TestDir "worktrees"

    # Set environment variables
    $env:WT_BIN = $script:WtBin
    $env:TEST_DIR = $script:TestDir
    $env:REPO_DIR = $script:RepoDir
    $env:REPO_NAME = $script:RepoName
    $env:WORKTREE_ROOT = $script:WorktreeRoot

    New-Item -ItemType Directory -Path $script:RepoDir -Force | Out-Null
    New-Item -ItemType Directory -Path $script:WorktreeRoot -Force | Out-Null

    # Initialize git repo
    Push-Location $script:RepoDir
    git init --quiet
    git config user.email "test@example.com"
    git config user.name "Test User"
    git commit --allow-empty -m "initial commit" --quiet
    git branch -M main
    Pop-Location

    Write-Host "Test environment initialized" -ForegroundColor Green
    Write-Host "  TEST_DIR:      $script:TestDir"
    Write-Host "  REPO_DIR:      $script:RepoDir"
    Write-Host "  WORKTREE_ROOT: $script:WorktreeRoot"
    Write-Host "  WT_BIN:        $script:WtBin"
}

# Cleanup test environment
function Clear-E2ETest {
    if ($script:TestDir -and (Test-Path $script:TestDir)) {
        Remove-Item -Recurse -Force $script:TestDir -ErrorAction SilentlyContinue
        Write-Host "Test environment cleaned up" -ForegroundColor Green
    }
}

# Setup step: create a branch
function New-TestBranch {
    param([string]$BranchName)

    Push-Location $script:RepoDir
    git checkout -b $BranchName --quiet
    git commit --allow-empty -m "commit on $BranchName" --quiet
    git checkout main --quiet
    Pop-Location
    Write-Host "  Created branch: $BranchName"
}

# Setup step: create a file
function New-TestFile {
    param(
        [string]$Path,
        [string]$Content
    )

    $fullPath = Join-Path $script:RepoDir $Path
    Set-Content -Path $fullPath -Value $Content
    Write-Host "  Created file: $Path"
}

# Setup step: git add
function Add-TestFile {
    param([string]$Path)

    Push-Location $script:RepoDir
    git add $Path
    Pop-Location
    Write-Host "  Staged: $Path"
}

# Setup step: git commit
function Save-TestCommit {
    param([string]$Message)

    Push-Location $script:RepoDir
    git commit -m $Message --quiet
    Pop-Location
    Write-Host "  Committed: $Message"
}

# Setup step: git checkout
function Switch-TestBranch {
    param([string]$BranchName)

    Push-Location $script:RepoDir
    git checkout $BranchName --quiet
    Pop-Location
    Write-Host "  Checked out: $BranchName"
}

# Reset to repo directory
function Enter-RepoDir {
    Set-Location $script:RepoDir
}

# Source wt shellenv
function Initialize-WtShellenv {
    $shellenvOutput = & $script:WtBin shellenv
    Invoke-Expression ($shellenvOutput -join "`n")
    Write-Host "  Sourced shellenv"
}

# Assertion: check exit code
function Assert-ExitCode {
    param(
        [int]$Expected,
        [int]$Actual
    )

    if ($Actual -ne $Expected) {
        Write-Host "FAIL: Expected exit code $Expected, got $Actual" -ForegroundColor Red
        return $false
    }
    return $true
}

# Assertion: check current working directory ends with
function Assert-CwdEndsWith {
    param([string]$Suffix)

    $cwd = (Get-Location).Path
    if (-not $cwd.EndsWith($Suffix)) {
        Write-Host "FAIL: Expected cwd to end with '$Suffix', got '$cwd'" -ForegroundColor Red
        return $false
    }
    return $true
}

# Assertion: check current branch
function Assert-Branch {
    param([string]$Expected)

    $actual = git branch --show-current
    if ($actual -ne $Expected) {
        Write-Host "FAIL: Expected branch '$Expected', got '$actual'" -ForegroundColor Red
        return $false
    }
    return $true
}

# Assertion: check output contains string
function Assert-OutputContains {
    param(
        [string]$Needle,
        [string]$Haystack
    )

    if (-not $Haystack.Contains($Needle)) {
        Write-Host "FAIL: Expected output to contain '$Needle'" -ForegroundColor Red
        Write-Host "Got: $Haystack" -ForegroundColor Red
        return $false
    }
    return $true
}

# Assertion: check output does not contain string
function Assert-OutputNotContains {
    param(
        [string]$Needle,
        [string]$Haystack
    )

    if ($Haystack.Contains($Needle)) {
        Write-Host "FAIL: Expected output to NOT contain '$Needle'" -ForegroundColor Red
        Write-Host "Got: $Haystack" -ForegroundColor Red
        return $false
    }
    return $true
}

# Print pass message
function Write-E2EPass {
    param([string]$Name)
    Write-Host "PASS: $Name" -ForegroundColor Green
}

# Print fail message
function Write-E2EFail {
    param(
        [string]$Name,
        [string]$Reason = ""
    )
    Write-Host "FAIL: $Name" -ForegroundColor Red
    if ($Reason) {
        Write-Host "  Reason: $Reason"
    }
}

# Print skip message
function Write-E2ESkip {
    param(
        [string]$Name,
        [string]$Reason = ""
    )
    Write-Host "SKIP: $Name" -ForegroundColor Yellow
    if ($Reason) {
        Write-Host "  Reason: $Reason"
    }
}
