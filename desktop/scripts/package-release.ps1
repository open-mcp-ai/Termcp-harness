[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$CoreSourceDir,

    [string]$OutputRoot,

    [string]$PackageName,

    [switch]$AllowDirty,

    [switch]$SkipTests,

    [switch]$SmokeTest
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
if ([string]::IsNullOrWhiteSpace($OutputRoot)) {
    $OutputRoot = Join-Path $repoRoot "desktop\build\releases"
}
$OutputRoot = [IO.Path]::GetFullPath($OutputRoot)
$CoreSourceDir = [IO.Path]::GetFullPath(
    (Resolve-Path -LiteralPath $CoreSourceDir -ErrorAction Stop).Path
)

function Invoke-Checked {
    param(
        [Parameter(Mandatory = $true)]
        [string]$FilePath,

        [string[]]$Arguments = @(),

        [string]$WorkingDirectory = $repoRoot
    )

    Push-Location -LiteralPath $WorkingDirectory
    try {
        & $FilePath @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$FilePath $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
}

function Get-GitStatus {
    param([string]$WorkingDirectory)

    $status = @(& git -C $WorkingDirectory status --porcelain)
    if ($LASTEXITCODE -ne 0) {
        throw "git status failed for $WorkingDirectory"
    }
    return [pscustomobject]@{
        Commit = (& git -C $WorkingDirectory rev-parse HEAD).Trim()
        Dirty  = $status.Count -gt 0
    }
}

function Get-RelativeChecksums {
    param(
        [string]$Root,
        [string[]]$ExcludeNames
    )

    $excluded = [Collections.Generic.HashSet[string]]::new(
        [StringComparer]::OrdinalIgnoreCase
    )
    foreach ($name in $ExcludeNames) {
        [void]$excluded.Add($name)
    }

    $entries = foreach ($file in Get-ChildItem -LiteralPath $Root -Recurse -File) {
        if ($excluded.Contains($file.Name)) {
            continue
        }
        $relative = [IO.Path]::GetRelativePath($Root, $file.FullName).Replace("\", "/")
        [pscustomobject]@{
            path   = $relative
            size   = $file.Length
            sha256 = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
        }
    }
    return @($entries | Sort-Object path)
}

function Stop-PackagedSmokeProcesses {
    param(
        [string]$ProductDir,
        [System.Diagnostics.Process]$DesktopProcess
    )

    $coreExe = [IO.Path]::GetFullPath((Join-Path $ProductDir "core\termcp-core.exe"))
    if ($DesktopProcess -and -not $DesktopProcess.HasExited) {
        Stop-Process -Id $DesktopProcess.Id -Force -ErrorAction SilentlyContinue
    }
    Get-Process -ErrorAction SilentlyContinue |
        Where-Object {
            $_.Path -and
            [string]::Equals(
                [IO.Path]::GetFullPath($_.Path),
                $coreExe,
                [StringComparison]::OrdinalIgnoreCase
            )
        } |
        ForEach-Object {
            Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
        }
}

function Remove-TemporaryTree {
    param([string]$Path)

    if (-not (Test-Path -LiteralPath $Path)) {
        return
    }
    $resolved = [IO.Path]::GetFullPath($Path)
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    if (-not $resolved.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "temporary cleanup target escapes the system temp directory: $resolved"
    }
    for ($attempt = 0; $attempt -lt 20; $attempt++) {
        try {
            Remove-Item -LiteralPath $resolved -Recurse -Force -ErrorAction Stop
            return
        }
        catch {
            if ($attempt -eq 19) {
                Write-Warning "temporary cleanup deferred because a process still holds $resolved"
                return
            }
            Start-Sleep -Milliseconds 250
        }
    }
}

function Test-PackageLaunch {
    param([string]$ProductDir)

    $desktopExe = Join-Path $ProductDir "Termcp Harness.exe"
    $coreExe = Join-Path $ProductDir "core\termcp-core.exe"
    if (-not (Test-Path -LiteralPath $desktopExe -PathType Leaf)) {
        throw "Smoke test Desktop executable is missing: $desktopExe"
    }
    if (-not (Test-Path -LiteralPath $coreExe -PathType Leaf)) {
        throw "Smoke test Core executable is missing: $coreExe"
    }
    $existing = Get-Process -Name "termcp-harness-desktop" -ErrorAction SilentlyContinue
    if ($existing) {
        throw "Desktop smoke test requires no running termcp-harness-desktop process"
    }

    $oldAppData = $env:APPDATA
    $oldLocalAppData = $env:LOCALAPPDATA
    $oldWebView = $env:TERMCP_HARNESS_WEBVIEW_DATA_PATH
    $smokeRoot = Join-Path ([IO.Path]::GetTempPath()) (
        "termcp-harness-package-smoke-" + [guid]::NewGuid().ToString("N")
    )
    New-Item -ItemType Directory -Path $smokeRoot | Out-Null
    $desktopProcess = $null
    try {
        $env:APPDATA = Join-Path $smokeRoot "AppData\Roaming"
        $env:LOCALAPPDATA = Join-Path $smokeRoot "AppData\Local"
        $env:TERMCP_HARNESS_WEBVIEW_DATA_PATH = Join-Path $smokeRoot "WebView2"
        New-Item -ItemType Directory -Path $env:APPDATA,$env:LOCALAPPDATA |
            Out-Null

        $desktopProcess = Start-Process -FilePath $desktopExe -PassThru
        $desktopLog = Join-Path $env:APPDATA "termcp-harness\logs\desktop.log"
        $deadline = [DateTime]::UtcNow.AddSeconds(45)
        $ready = $false
        while ([DateTime]::UtcNow -lt $deadline) {
            if ($desktopProcess.HasExited) {
                break
            }
            $desktopProcess.Refresh()
            if ($desktopProcess.MainWindowHandle -ne 0 -and
                (Test-Path -LiteralPath $desktopLog -PathType Leaf)) {
                $logText = Get-Content -LiteralPath $desktopLog -Raw -Encoding UTF8
                if ($logText -match '"msg":"bundled Core identity verified"' -and
                    $logText -match '"ownership":"managed"') {
                    $ready = $true
                    break
                }
            }
            Start-Sleep -Milliseconds 500
        }
        if (-not $ready) {
            $detail = ""
            if (Test-Path -LiteralPath $desktopLog -PathType Leaf) {
                $detail = (Get-Content -LiteralPath $desktopLog -Tail 30 -Encoding UTF8) -join [Environment]::NewLine
            }
            throw "Packaged Desktop did not reach managed Core readiness.`n$detail"
        }

        $core = Get-Process -ErrorAction SilentlyContinue |
            Where-Object {
                $_.Path -and
                [string]::Equals(
                    [IO.Path]::GetFullPath($_.Path),
                    [IO.Path]::GetFullPath($coreExe),
                    [StringComparison]::OrdinalIgnoreCase
                )
            } |
            Select-Object -First 1
        if (-not $core) {
            throw "Packaged Desktop reported readiness but no bundled Core process was found"
        }
    }
    finally {
        Stop-PackagedSmokeProcesses -ProductDir $ProductDir -DesktopProcess $desktopProcess
        $env:APPDATA = $oldAppData
        $env:LOCALAPPDATA = $oldLocalAppData
        $env:TERMCP_HARNESS_WEBVIEW_DATA_PATH = $oldWebView
        Remove-TemporaryTree -Path $smokeRoot
    }
}

$harnessGit = Get-GitStatus -WorkingDirectory $repoRoot
$coreGit = Get-GitStatus -WorkingDirectory $CoreSourceDir
if (($harnessGit.Dirty -or $coreGit.Dirty) -and -not $AllowDirty) {
    throw @"
Packaging refuses dirty sources.
Harness: $($harnessGit.Commit) dirty=$($harnessGit.Dirty)
Core:    $($coreGit.Commit) dirty=$($coreGit.Dirty)
Commit or otherwise freeze both sources, or pass -AllowDirty for a candidate build.
"@
}

$wailsConfigPath = Join-Path $repoRoot "desktop\wails.json"
$wailsConfig = Get-Content -LiteralPath $wailsConfigPath -Raw -Encoding UTF8 |
    ConvertFrom-Json
$productVersion = "v$($wailsConfig.info.productVersion)"
$wailsVersion = ((& wails version | Select-Object -First 1) -join "").Trim()
$goVersion = (& go version).Trim()
$nodeVersion = (& node --version).Trim()
$artifactState = if ($harnessGit.Dirty -or $coreGit.Dirty) {
    "candidate"
}
else {
    "release-candidate"
}

$buildRoot = Join-Path ([IO.Path]::GetTempPath()) (
    "termcp-harness-package-" + [guid]::NewGuid().ToString("N")
)
New-Item -ItemType Directory -Path $buildRoot | Out-Null
$oldGoCache = $env:GOCACHE
$oldGoTmp = $env:GOTMPDIR
$oldNpmCache = $env:npm_config_cache
try {
    $env:GOCACHE = Join-Path $buildRoot "gocache"
    $env:GOTMPDIR = Join-Path $buildRoot "gotmp"
    $env:npm_config_cache = Join-Path $buildRoot "npm-cache"
    New-Item -ItemType Directory -Path $env:GOCACHE,$env:GOTMPDIR,$env:npm_config_cache |
        Out-Null

    $coreBuildDir = Join-Path $buildRoot "core-build"
    New-Item -ItemType Directory -Path $coreBuildDir | Out-Null
    $coreExe = Join-Path $coreBuildDir "termcp.exe"
    Invoke-Checked -FilePath "go" -Arguments @(
        "build", "-trimpath", "-o", $coreExe, "."
    ) -WorkingDirectory $CoreSourceDir
    $versionOutput = (& $coreExe -version | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "Core version command failed"
    }
    $versionMatch = [regex]::Match($versionOutput, "(?m)\btermcp\s+(\S+)\s*$")
    if (-not $versionMatch.Success) {
        throw "Could not parse Core version from: $versionOutput"
    }
    $coreVersion = $versionMatch.Groups[1].Value

    if (-not $SkipTests) {
        Invoke-Checked -FilePath "go" -Arguments @(
            "test", "-race", "./...", "-count=1"
        )
        Invoke-Checked -FilePath "go" -Arguments @("vet", "./...")
        Invoke-Checked -FilePath "go" -Arguments @("build", "./...")
        $frontendDir = Join-Path $repoRoot "desktop\frontend"
        Invoke-Checked -FilePath "npm" -Arguments @("ci") -WorkingDirectory $frontendDir
        Invoke-Checked -FilePath "npm" -Arguments @("run", "check") -WorkingDirectory $frontendDir
        Invoke-Checked -FilePath "npm" -Arguments @("test") -WorkingDirectory $frontendDir
    }

    $frontendDir = Join-Path $repoRoot "desktop\frontend"
    Invoke-Checked -FilePath "npm" -Arguments @("run", "build") -WorkingDirectory $frontendDir
    $desktopDir = Join-Path $repoRoot "desktop"
    Invoke-Checked -FilePath "wails" -Arguments @(
        "build", "-m", "-nosyncgomod", "-clean", "-s", "-nocolour"
    ) -WorkingDirectory $desktopDir

    $safeCoreVersion = $coreVersion -replace "[^A-Za-z0-9._+-]", "-"
    if ([string]::IsNullOrWhiteSpace($PackageName)) {
        $PackageName = "Termcp-Harness-$productVersion+core.$safeCoreVersion-windows-amd64"
    }
    $productDir = Join-Path $OutputRoot $PackageName
    if (Test-Path -LiteralPath $productDir) {
        throw "Package output already exists: $productDir"
    }
    New-Item -ItemType Directory -Path (Join-Path $productDir "core") -Force |
        Out-Null

    $desktopBuild = Join-Path $desktopDir "build\bin\termcp-harness-desktop.exe"
    Copy-Item -LiteralPath $desktopBuild -Destination (
        Join-Path $productDir "Termcp Harness.exe"
    )
    Invoke-Checked -FilePath "go" -Arguments @(
        "run", "./cmd/termcp-harness", "core", "bundle",
        "--binary", $coreExe,
        "--version", $coreVersion,
        "--out", (Join-Path $productDir "core")
    )
    foreach ($document in @("README.md", "CHANGELOG.md")) {
        $source = Join-Path $repoRoot $document
        if (Test-Path -LiteralPath $source -PathType Leaf) {
            Copy-Item -LiteralPath $source -Destination (Join-Path $productDir $document)
        }
    }

    $artifactEntries = Get-RelativeChecksums -Root $productDir -ExcludeNames @(
        "release.json", "checksums.txt"
    )
    $release = [ordered]@{
        schema                   = 1
        product                  = "Termcp Harness"
        product_version          = $productVersion
        artifact_state           = $artifactState
        platform                 = "windows-amd64"
        built_at                 = [DateTime]::UtcNow.ToString("o")
        toolchain                = [ordered]@{
            go    = $goVersion
            node  = $nodeVersion
            wails = $wailsVersion
        }
        harness                  = [ordered]@{
            commit = $harnessGit.Commit
            dirty  = $harnessGit.Dirty
        }
        core                     = [ordered]@{
            version = $coreVersion
            commit  = $coreGit.Commit
            dirty   = $coreGit.Dirty
            sha256  = (Get-FileHash -LiteralPath (
                Join-Path $productDir "core\termcp-core.exe"
            ) -Algorithm SHA256).Hash
        }
        desktop_entrypoint       = "Termcp Harness.exe"
        core_bundle_manifest     = "core/manifest.json"
        artifacts                = $artifactEntries
    }
    $release |
        ConvertTo-Json -Depth 8 |
        Set-Content -LiteralPath (Join-Path $productDir "release.json") -Encoding utf8

    $allEntries = Get-RelativeChecksums -Root $productDir -ExcludeNames @("checksums.txt")
    $checksumLines = $allEntries | ForEach-Object {
        "$($_.sha256)  $($_.path)"
    }
    $checksumLines |
        Set-Content -LiteralPath (Join-Path $productDir "checksums.txt") -Encoding ascii

    if ($SmokeTest) {
        Test-PackageLaunch -ProductDir $productDir
    }

    $zipPath = Join-Path $OutputRoot "$PackageName.zip"
    if (Test-Path -LiteralPath $zipPath) {
        throw "Package ZIP already exists: $zipPath"
    }
    Compress-Archive -LiteralPath $productDir -DestinationPath $zipPath -CompressionLevel Optimal
    $zipHash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash
    "$zipHash  $([IO.Path]::GetFileName($zipPath))" |
        Set-Content -LiteralPath "$zipPath.sha256" -Encoding ascii

    Write-Output "package=$productDir"
    Write-Output "zip=$zipPath"
    Write-Output "zip_sha256=$zipHash"
    Write-Output "artifact_state=$artifactState"
    Write-Output "core_version=$coreVersion"
}
finally {
    $env:GOCACHE = $oldGoCache
    $env:GOTMPDIR = $oldGoTmp
    $env:npm_config_cache = $oldNpmCache
    Remove-TemporaryTree -Path $buildRoot
}
