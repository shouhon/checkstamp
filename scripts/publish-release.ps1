[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('\A[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\z')]
    [string]$Repository,
    [Parameter(Mandatory)]
    [ValidatePattern('\A[0-9a-f]{40}\z')]
    [string]$Commit,
    [Parameter(Mandatory)]
    [ValidatePattern('\A(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\z')]
    [string]$Version,
    [Parameter(Mandatory)]
    [string]$AssetDirectory
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$tag = "v$Version"
$apiRoot = "repos/$Repository"
$archiveName = "checkstamp_${Version}_windows_amd64.zip"
$assetNames = @($archiveName, 'checkstamp.json', 'checksums.txt')
$assets = @($assetNames | ForEach-Object { Join-Path $AssetDirectory $_ })
foreach ($asset in $assets) {
    if (-not (Test-Path -LiteralPath $asset -PathType Leaf) -or (Get-Item -LiteralPath $asset).Length -eq 0) {
        throw '公開する配布ファイルが不足しています。'
    }
}
$expectedChecksums = @($assets[0..1] | ForEach-Object {
    $hash = (Get-FileHash -LiteralPath $_ -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  $(Split-Path -Leaf $_)"
})
$checksums = @(Get-Content -LiteralPath $assets[2])
if (($checksums -join "`n") -cne ($expectedChecksums -join "`n")) {
    throw '配布ファイルのSHA-256が一致しません。'
}
$manifest = Get-Content -LiteralPath $assets[1] -Raw | ConvertFrom-Json
if ($manifest.version -cne $Version -or $manifest.architecture.'64bit'.url -cne "https://github.com/$Repository/releases/download/$tag/$archiveName") {
    throw 'Scoop定義の版番号または配布先が一致しません。'
}

# 存在しない場合の空結果と、認証・通信の失敗を区別する。
$releaseText = & gh api "$apiRoot/releases?per_page=100" --paginate --jq ".[] | select(.tag_name == `"$tag`") | {id, draft, target_commitish}"
if ($LASTEXITCODE -ne 0) {
    throw '既存Releaseを確認できません。'
}
$release = if ($releaseText) { ($releaseText -join "`n") | ConvertFrom-Json } else { $null }
$tagText = & gh api "$apiRoot/git/matching-refs/tags/$tag" --jq ".[] | select(.ref == `"refs/tags/$tag`") | .ref"
if ($LASTEXITCODE -ne 0) {
    throw '既存タグを確認できません。'
}
if ($tagText) {
    # 注釈付きタグもコミットへ解決して照合する。
    $tagCommit = & gh api "$apiRoot/commits/$tag" --jq .sha
    if ($LASTEXITCODE -ne 0 -or $tagCommit -cne $Commit) {
        throw '既存タグが公開対象のコミットと一致しません。'
    }
}
elseif ($null -ne $release -and (-not $release.draft -or $release.target_commitish -cne $Commit)) {
    throw '既存Releaseの公開対象を確認できません。'
}

if ($null -ne $release -and -not $release.draft) {
    Write-Host "$tag は公開済みです。"
    return
}
if (-not $tagText) {
    & gh api "$apiRoot/git/refs" --method POST -f "ref=refs/tags/$tag" -f "sha=$Commit" --silent
    if ($LASTEXITCODE -ne 0) {
        throw 'タグを作成できません。'
    }
}
if ($null -eq $release) {
    & gh release create $tag --repo $Repository --target $Commit --verify-tag --draft --title "checkstamp $Version" --generate-notes
    if ($LASTEXITCODE -ne 0) {
        throw 'Releaseの下書きを作成できません。'
    }
}

# タグ名によるREST取得は公開済みRelease用なので、下書きにも対応するCLIで取得する。
$draftText = & gh release view $tag --repo $Repository --json databaseId,isDraft
if ($LASTEXITCODE -ne 0) {
    throw '公開する下書きを取得できません。'
}
$draft = ($draftText -join "`n") | ConvertFrom-Json
if ($draft.isDraft -isnot [bool] -or -not $draft.isDraft -or "$($draft.databaseId)" -cnotmatch '\A[1-9][0-9]*\z') {
    throw '公開するReleaseが下書きではありません。'
}
$releaseId = $draft.databaseId

# 上書きが許されるのは未公開の下書きだけ。途中失敗後も同じコミットで再開できる。
& gh release upload $tag @assets --repo $Repository --clobber
if ($LASTEXITCODE -ne 0) {
    throw '配布ファイルを添付できません。Releaseは下書きのままです。'
}
# 後から完了した古い版で最新版を戻さないよう、GitHubの版番号による判定を使う。
& gh api "$apiRoot/releases/$releaseId" --method PATCH -F draft=false -f make_latest=legacy --silent
if ($LASTEXITCODE -ne 0) {
    throw 'Releaseを公開できません。'
}
Write-Host "公開完了: https://github.com/$Repository/releases/tag/$tag"
