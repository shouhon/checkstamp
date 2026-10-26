[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$projectRoot = Split-Path -Parent $PSScriptRoot

Push-Location -LiteralPath $projectRoot
try {
    Write-Host 'Goモジュールの整合性を確認します。'
    & go mod tidy -diff
    if ($LASTEXITCODE -ne 0) {
        throw "go.mod の整合性確認に失敗しました（終了コード: $LASTEXITCODE）。"
    }

    Write-Host 'Goファイルの整形を確認します。'
    $packageDirs = @(& go list -f '{{.Dir}}' ./...)
    if ($LASTEXITCODE -ne 0) {
        throw "パッケージの一覧取得に失敗しました（終了コード: $LASTEXITCODE）。"
    }
    $goFiles = @($packageDirs | ForEach-Object {
        Get-ChildItem -LiteralPath $_ -Filter '*.go' -File | Select-Object -ExpandProperty FullName
    })
    if ($goFiles.Count -eq 0) {
        throw '検査対象のGoファイルがありません。'
    }
    $unformatted = @(& gofmt -l @goFiles)
    if ($LASTEXITCODE -ne 0) {
        throw "gofmt の実行に失敗しました（終了コード: $LASTEXITCODE）。"
    }
    if ($unformatted.Count -ne 0) {
        $unformatted | ForEach-Object { Write-Host $_ }
        throw '未整形のGoファイルがあります。gofmt -w ./cmd ./internal を実行してください。'
    }

    Write-Host '静的検査を実行します。'
    & go vet ./...
    if ($LASTEXITCODE -ne 0) {
        throw "go vet に失敗しました（終了コード: $LASTEXITCODE）。"
    }

    Write-Host 'テストを実行します。'
    & go test -count=1 ./...
    if ($LASTEXITCODE -ne 0) {
        throw "go test に失敗しました（終了コード: $LASTEXITCODE）。"
    }

    Write-Host '自動リリースの判定と失敗時の処理を確認します。'
    & (Join-Path $PSScriptRoot 'test-release.ps1')

    & (Join-Path $PSScriptRoot 'build.ps1')
    & './dist/checkstamp.exe' --help > $null
    if ($LASTEXITCODE -ne 0) {
        throw "実行ファイルのヘルプ表示に失敗しました（終了コード: $LASTEXITCODE）。"
    }
    Write-Host 'すべての検査に成功しました。'
}
finally {
    Pop-Location
}
