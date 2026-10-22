[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$projectRoot = Split-Path -Parent $PSScriptRoot

Push-Location -LiteralPath $projectRoot
try {
    if (-not $IsWindows) {
        throw '配布物はWindows上で作成してください。'
    }
    $target = @(& go env GOOS GOARCH)
    if ($LASTEXITCODE -ne 0 -or $target.Count -ne 2 -or $target[0] -cne 'windows' -or $target[1] -cne 'amd64') {
        throw '配布対象はWindows/amd64です。Goのビルド対象を確認してください。'
    }

    & (Join-Path $PSScriptRoot 'check.ps1')
    $version = (Get-Content -LiteralPath 'internal/version/VERSION' -Raw).Trim()
    $releaseDir = Join-Path $projectRoot "dist/release/$version"
    New-Item -ItemType Directory -Path $releaseDir -Force | Out-Null

    $archiveName = "checkstamp_${version}_windows_amd64.zip"
    $archivePath = Join-Path $releaseDir $archiveName
    Compress-Archive -LiteralPath 'dist/checkstamp.exe', 'LICENSE' -DestinationPath $archivePath -Force
    $archiveHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()

    $manifest = [ordered]@{
        version = $version
        description = '検証コマンドの結果と、明示したファイルの状態を結び付けて保存するCLI'
        homepage = 'https://github.com/shouhon/checkstamp'
        license = 'MIT'
        architecture = [ordered]@{
            '64bit' = [ordered]@{
                url = "https://github.com/shouhon/checkstamp/releases/download/v$version/$archiveName"
                hash = $archiveHash
            }
        }
        bin = 'checkstamp.exe'
    }
    $manifestPath = Join-Path $releaseDir 'checkstamp.json'
    $manifest | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $manifestPath -Encoding utf8NoBOM
    $manifestHash = (Get-FileHash -LiteralPath $manifestPath -Algorithm SHA256).Hash.ToLowerInvariant()
    @(
        "$archiveHash  $archiveName"
        "$manifestHash  checkstamp.json"
    ) | Set-Content -LiteralPath (Join-Path $releaseDir 'checksums.txt') -Encoding utf8NoBOM

    Write-Host "配布準備完了: dist/release/$version"
    Write-Host 'ZIP、checkstamp.json、checksums.txtを作成しました。'
}
finally {
    Pop-Location
}
