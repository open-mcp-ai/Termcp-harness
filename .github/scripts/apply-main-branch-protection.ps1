[CmdletBinding()]
param(
    [string]$Repository = "open-mcp-ai/Termcp-harness",
    [string]$Branch = "main",
    [string]$ConfigPath = (Join-Path $PSScriptRoot "..\main-branch-protection.json")
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ConfigPath = [IO.Path]::GetFullPath(
    (Resolve-Path -LiteralPath $ConfigPath -ErrorAction Stop).Path
)

& gh api `
    --method PUT `
    "repos/$Repository/branches/$Branch/protection" `
    --input $ConfigPath
if ($LASTEXITCODE -ne 0) {
    throw "Failed to apply branch protection to $Repository/$Branch"
}

& gh api "repos/$Repository/branches/$Branch/protection"
if ($LASTEXITCODE -ne 0) {
    throw "Branch protection was submitted but could not be read back"
}

Write-Output "main branch protection applied: $Repository/$Branch"
