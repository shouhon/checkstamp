[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$versionScript = Join-Path $PSScriptRoot 'release-version.ps1'
$publishScript = Join-Path $PSScriptRoot 'publish-release.ps1'
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("checkstamp-release-test-" + [guid]::NewGuid().ToString('N'))
$gitRoot = Join-Path $testRoot 'repository'
$emptyHooks = Join-Path $testRoot 'empty-hooks'
$emptyAttributes = Join-Path $testRoot 'empty-attributes'
$assetDirectory = Join-Path $testRoot 'assets'
$targetCommit = 'a' * 40
$script:releaseTestCount = 0

function Assert-ReleaseTest([bool]$Condition, [string]$Description) {
    if (-not $Condition) { throw "自動リリースの試験に失敗しました: $Description" }
    $script:releaseTestCount++
}

function Assert-ReleaseFailure([scriptblock]$Action, [string]$ExpectedMessage) {
    $caught = $false
    try { & $Action | Out-Null }
    catch {
        if ($_.Exception.Message -notlike $ExpectedMessage) { throw }
        $caught = $true
    }
    Assert-ReleaseTest $caught $ExpectedMessage
}

function Invoke-TestGit {
    # 利用者の共通フック・属性・監視コマンドを試験から起動しない。
    $result = & git -c "core.hooksPath=$emptyHooks" -c "core.attributesFile=$emptyAttributes" -c core.fsmonitor=false -C $gitRoot @args
    if ($LASTEXITCODE -ne 0) { throw '試験用のGit操作に失敗しました。' }
    $result
}

function New-TestCommit {
    Invoke-TestGit add --all
    Invoke-TestGit -c user.name=checkstamp-test -c user.email=test@example.invalid -c commit.gpgsign=false commit --quiet --allow-empty -m '自動リリースの試験'
    Invoke-TestGit rev-parse HEAD
}

function Reset-ReleaseState {
    $script:releaseState = @{
        Release = $null
        TagExists = $false
        TagCommit = $targetCommit
        FailAt = ''
        DraftView = @{ databaseId = 17; isDraft = $true }
        Calls = [Collections.Generic.List[object]]::new()
        TagCreated = $false
        DraftCreated = $false
        Uploaded = $false
        Published = $false
    }
}

# 実際のGitHub CLIより優先する関数を、この試験のスコープだけに置く。
function gh {
    $callArgs = @($args)
    $releaseState.Calls.Add($callArgs)
    Set-Variable -Name LASTEXITCODE -Value 0 -Scope 1
    $operation = switch ($callArgs[0]) {
        'api' {
            switch -Wildcard ($callArgs[1]) {
                '*/releases?per_page=100' { 'query'; break }
                '*/git/matching-refs/tags/*' { 'tag'; break }
                '*/commits/*' { 'commit'; break }
                '*/git/refs' { 'create-tag'; break }
                '*/releases/17' { 'publish'; break }
                default { throw '想定外のGitHub API呼び出しです。' }
            }
            break
        }
        'release' {
            switch ($callArgs[1]) {
                'create' { 'create-draft'; break }
                'view' { 'draft-view'; break }
                'upload' { 'upload'; break }
                default { throw '想定外のRelease操作です。' }
            }
            break
        }
        default { throw '想定外のGitHub CLI呼び出しです。' }
    }
    if ($releaseState.FailAt -ceq $operation) {
        Set-Variable -Name LASTEXITCODE -Value 1 -Scope 1
        return
    }
    switch ($operation) {
        'query' { if ($null -ne $releaseState.Release) { $releaseState.Release | ConvertTo-Json -Compress } }
        'tag' { if ($releaseState.TagExists) { 'refs/tags/v0.4.2' } }
        'commit' { $releaseState.TagCommit }
        'create-tag' { $releaseState.TagCreated = $true }
        'create-draft' { $releaseState.DraftCreated = $true }
        'upload' { $releaseState.Uploaded = $true }
        'draft-view' { $releaseState.DraftView | ConvertTo-Json -Compress }
        'publish' { $releaseState.Published = $true }
    }
}

function Invoke-TestPublish {
    & $publishScript -Repository 'example/checkstamp' -Commit $targetCommit -Version '0.4.2' -AssetDirectory $assetDirectory
}

try {
    New-Item -ItemType Directory -Path (Join-Path $gitRoot 'internal/version'), $assetDirectory, $emptyHooks -Force | Out-Null
    Set-Content -LiteralPath $emptyAttributes -Value '' -NoNewline -Encoding utf8NoBOM
    Invoke-TestGit -c init.defaultBranch=main init --quiet "--template=$emptyHooks"
    Invoke-TestGit config core.autocrlf false
    Set-Content -LiteralPath (Join-Path $gitRoot 'README.md') -Value '試験用' -Encoding utf8NoBOM
    $withoutVersion = New-TestCommit
    $versionFile = Join-Path $gitRoot 'internal/version/VERSION'
    Set-Content -LiteralPath $versionFile -Value '0.4.1' -Encoding utf8NoBOM
    $before = New-TestCommit
    Set-Content -LiteralPath $versionFile -Value '0.4.2' -Encoding utf8NoBOM
    $bumped = New-TestCommit
    $after = New-TestCommit

    Push-Location -LiteralPath $gitRoot
    try {
        $result = & $versionScript -BeforeCommit $before -Commit $after
        Assert-ReleaseTest ($result.Changed -and $result.Version -ceq '0.4.2') '複数コミットのpushで途中の版更新を検出する'
        $result = & $versionScript -BeforeCommit $bumped -Commit $after
        Assert-ReleaseTest (-not $result.Changed) '版番号が同じpushでは公開しない'
        Set-Content -LiteralPath $versionFile -Value "0.4.2`n" -NoNewline -Encoding utf8NoBOM
        $newlineOnly = New-TestCommit
        $result = & $versionScript -BeforeCommit $after -Commit $newlineOnly
        Assert-ReleaseTest (-not $result.Changed) '改行だけの変更では公開しない'
        $result = & $versionScript -BeforeCommit ('0' * 40) -Commit $before
        Assert-ReleaseTest $result.Changed 'mainの初回pushを扱う'
        $result = & $versionScript -BeforeCommit $withoutVersion -Commit $before
        Assert-ReleaseTest $result.Changed '版番号ファイルの追加を検出する'
        Assert-ReleaseFailure { & $versionScript -BeforeCommit ('f' * 40) -Commit $after 2>$null } '*push前のコミットを取得できません*'
        Assert-ReleaseFailure { & $versionScript -BeforeCommit $before -Commit ('f' * 40) 2>$null } '*push後の版番号を取得できません*'
        Set-Content -LiteralPath $versionFile -Value '0.04.2' -Encoding utf8NoBOM
        $invalid = New-TestCommit
        Assert-ReleaseFailure { & $versionScript -BeforeCommit $before -Commit $invalid } '*push後の版番号が正しくありません*'
        Assert-ReleaseFailure { & $versionScript -BeforeCommit $invalid -Commit $after } '*push前の版番号が正しくありません*'
    }
    finally { Pop-Location }

    $archivePath = Join-Path $assetDirectory 'checkstamp_0.4.2_windows_amd64.zip'
    $manifestPath = Join-Path $assetDirectory 'checkstamp.json'
    $checksumsPath = Join-Path $assetDirectory 'checksums.txt'
    Set-Content -LiteralPath $archivePath -Value '外部公開しない試験データ' -Encoding utf8NoBOM
    @{
        version = '0.4.2'
        architecture = @{ '64bit' = @{ url = 'https://github.com/example/checkstamp/releases/download/v0.4.2/checkstamp_0.4.2_windows_amd64.zip' } }
    } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $manifestPath -Encoding utf8NoBOM
    $checksums = @($archivePath, $manifestPath | ForEach-Object {
        "$((Get-FileHash -LiteralPath $_ -Algorithm SHA256).Hash.ToLowerInvariant())  $(Split-Path -Leaf $_)"
    })
    $checksums | Set-Content -LiteralPath $checksumsPath -Encoding utf8NoBOM

    Reset-ReleaseState
    Invoke-TestPublish
    Assert-ReleaseTest ($script:releaseState.TagCreated -and $script:releaseState.DraftCreated -and $script:releaseState.Uploaded -and $script:releaseState.Published) '新しい版を下書き経由で公開する'
    $tagCall = @($script:releaseState.Calls | Where-Object { $_ -contains "sha=$targetCommit" })
    Assert-ReleaseTest ($tagCall.Count -eq 1) '検査したコミットへタグを固定する'
    $uploadCall = @($script:releaseState.Calls | Where-Object { $_[0] -ceq 'release' -and $_[1] -ceq 'upload' })
    Assert-ReleaseTest ($uploadCall.Count -eq 1 -and $uploadCall[0].Count -eq 9 -and $uploadCall[0] -contains $archivePath -and $uploadCall[0] -contains $manifestPath -and $uploadCall[0] -contains $checksumsPath) '指定した3ファイルだけを添付する'
    $publishCall = @($script:releaseState.Calls | Where-Object { $_ -contains 'make_latest=legacy' })
    Assert-ReleaseTest ($publishCall.Count -eq 1) 'GitHubの版番号による最新版の判定を指定する'

    Reset-ReleaseState
    $script:releaseState.TagExists = $true
    $script:releaseState.Release = @{ id = 17; draft = $false; target_commitish = $targetCommit }
    Invoke-TestPublish
    Assert-ReleaseTest (-not $script:releaseState.Uploaded -and -not $script:releaseState.Published -and -not $script:releaseState.TagCreated) '公開済みの同じ版を変更しない'
    $script:releaseState.TagCommit = 'b' * 40
    Assert-ReleaseFailure { Invoke-TestPublish } '*既存タグが公開対象のコミットと一致しません*'
    Assert-ReleaseTest (-not $script:releaseState.Uploaded) '別コミットの公開済みファイルを上書きしない'

    Reset-ReleaseState
    $script:releaseState.Release = @{ id = 17; draft = $true; target_commitish = ('b' * 40) }
    Assert-ReleaseFailure { Invoke-TestPublish } '*既存Releaseの公開対象を確認できません*'
    Assert-ReleaseTest (-not $script:releaseState.TagCreated -and -not $script:releaseState.Uploaded) '対象不明の下書きを変更しない'

    Reset-ReleaseState
    $script:releaseState.TagExists = $true
    $script:releaseState.Release = @{ id = 17; draft = $true; target_commitish = $targetCommit }
    Invoke-TestPublish
    Assert-ReleaseTest (-not $script:releaseState.DraftCreated -and $script:releaseState.Uploaded -and $script:releaseState.Published) '同じコミットの下書きから再開する'

    foreach ($failure in @(
        @{ At = 'query'; Message = '*既存Releaseを確認できません*' },
        @{ At = 'tag'; Message = '*既存タグを確認できません*' },
        @{ At = 'create-tag'; Message = '*タグを作成できません*' },
        @{ At = 'create-draft'; Message = '*Releaseの下書きを作成できません*' },
        @{ At = 'upload'; Message = '*配布ファイルを添付できません*' },
        @{ At = 'draft-view'; Message = '*公開する下書きを取得できません*' },
        @{ At = 'publish'; Message = '*Releaseを公開できません*' }
    )) {
        Reset-ReleaseState
        $script:releaseState.FailAt = $failure.At
        Assert-ReleaseFailure { Invoke-TestPublish } $failure.Message
        Assert-ReleaseTest (-not $script:releaseState.Published) "$($failure.At) の失敗後に公開しない"
        if ($failure.At -in @('query', 'tag')) {
            Assert-ReleaseTest (-not $script:releaseState.TagCreated -and -not $script:releaseState.DraftCreated -and -not $script:releaseState.Uploaded) '確認失敗を未作成と解釈しない'
        }
    }

    Reset-ReleaseState
    $script:releaseState.DraftView.isDraft = $false
    Assert-ReleaseFailure { Invoke-TestPublish } '*公開するReleaseが下書きではありません*'
    Assert-ReleaseTest (-not $script:releaseState.Uploaded -and -not $script:releaseState.Published) '添付直前に公開済みと分かったReleaseを変更しない'

    Reset-ReleaseState
    Set-Content -LiteralPath $checksumsPath -Value '不一致' -Encoding utf8NoBOM
    Assert-ReleaseFailure { Invoke-TestPublish } '*配布ファイルのSHA-256が一致しません*'
    Assert-ReleaseTest ($script:releaseState.Calls.Count -eq 0) 'ハッシュ不一致では外部操作しない'
    $checksums | Set-Content -LiteralPath $checksumsPath -Encoding utf8NoBOM
    Assert-ReleaseFailure { & $publishScript -Repository 'example/checkstamp' -Commit $targetCommit -Version '0.4.3' -AssetDirectory $assetDirectory } '*公開する配布ファイルが不足しています*'
    Assert-ReleaseTest ($script:releaseState.Calls.Count -eq 0) '配布物不足では外部操作しない'

    Write-Host "自動リリースの試験に成功しました（$script:releaseTestCount 件、外部への書き込みなし）。"
}
finally {
    # この試験が作成した一意な一時ディレクトリだけを削除する。
    $resolvedRoot = [IO.Path]::GetFullPath($testRoot)
    $temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar)
    if ([IO.Path]::GetDirectoryName($resolvedRoot) -cne $temporaryRoot -or [IO.Path]::GetFileName($resolvedRoot) -notlike 'checkstamp-release-test-*') {
        throw '試験用ディレクトリの削除範囲を確認できません。'
    }
    if (Test-Path -LiteralPath $resolvedRoot) {
        Remove-Item -LiteralPath $resolvedRoot -Recurse -Force
    }
}
