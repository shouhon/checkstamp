[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$projectRoot = Split-Path -Parent $PSScriptRoot

Push-Location -LiteralPath $projectRoot
try {
    $version = (Get-Content -LiteralPath 'internal/version/VERSION' -Raw).Trim()
    if ($version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {
        throw 'internal/version/VERSION の版番号が正しくありません。'
    }

    New-Item -ItemType Directory -Path 'dist' -Force | Out-Null
    & go build -mod=readonly -trimpath -o './dist/checkstamp.exe' ./cmd/checkstamp
    if ($LASTEXITCODE -ne 0) {
        throw "ビルドに失敗しました（終了コード: $LASTEXITCODE）。"
    }

    $actualVersion = & './dist/checkstamp.exe' --version
    if ($LASTEXITCODE -ne 0 -or $actualVersion -cne "checkstamp $version") {
        throw '実行ファイルの版表示が internal/version/VERSION と一致しません。'
    }

    Write-Host "ビルド完了: dist/checkstamp.exe ($actualVersion)"
}
finally {
    Pop-Location
}
