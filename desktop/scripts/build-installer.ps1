[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$PackageDir,

    [string]$OutputDirectory
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$PackageDir = [IO.Path]::GetFullPath(
    (Resolve-Path -LiteralPath $PackageDir -ErrorAction Stop).Path
)
foreach ($relative in @(
    "Termcp Harness.exe",
    "termcp-harness.exe",
    "README.md",
    "CHANGELOG.md",
    "release.json",
    "checksums.txt",
    "core\manifest.json",
    "core\termcp-core.exe"
)) {
    if (-not (Test-Path -LiteralPath (Join-Path $PackageDir $relative) -PathType Leaf)) {
        throw "Installer source file is missing: $relative"
    }
}
if ($PackageDir.Contains('"') -or $PackageDir.Contains('$')) {
    throw "Package path contains characters that are unsafe for the NSIS template"
}

$makensis = Get-Command makensis.exe -ErrorAction SilentlyContinue
if (-not $makensis) {
    throw "makensis.exe is required and must be available on PATH"
}
$nsisDirectory = Split-Path -Parent $makensis.Source
$env:PATH = "$nsisDirectory;$env:PATH"

$installerDir = Join-Path $repoRoot "desktop\build\windows\installer"
New-Item -ItemType Directory -Path $installerDir -Force | Out-Null
$template = Get-Content -LiteralPath (Join-Path $repoRoot "desktop\installer\project.nsi") -Raw -Encoding UTF8
[IO.File]::WriteAllText(
    (Join-Path $installerDir "project.nsi"),
    $template.Replace("@@HARNESS_PACKAGE_DIR@@", $PackageDir),
    [Text.UTF8Encoding]::new($true)
)

Push-Location -LiteralPath (Join-Path $repoRoot "desktop")
try {
    & wails build -m -nosyncgomod -clean -s -nocolour -nsis -installscope user
    if ($LASTEXITCODE -ne 0) {
        throw "Wails installer build failed with exit code $LASTEXITCODE"
    }
}
finally {
    Pop-Location
}

$installer = Join-Path $repoRoot "desktop\build\bin\Harness_Install.exe"
if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) {
    throw "Harness_Install.exe was not produced"
}
if (-not [string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
    New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
    Copy-Item -LiteralPath $installer -Destination (
        Join-Path $OutputDirectory "Harness_Install.exe"
    ) -Force
}
Write-Output "installer=$installer"
