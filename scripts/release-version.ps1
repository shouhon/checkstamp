[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('\A[0-9a-f]{40}\z')]
    [string]$BeforeCommit,
    [Parameter(Mandatory)]
    [ValidatePattern('\A[0-9a-f]{40}\z')]
    [string]$Commit
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$versionPattern = '\A(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\z'
$versionPath = 'internal/version/VERSION'

$current = @(& git show "${Commit}:$versionPath")
if ($LASTEXITCODE -ne 0) {
    throw 'push後の版番号を取得できません。'
}
$version = ($current -join "`n").Trim()
if ($version -cnotmatch $versionPattern) {
    throw 'push後の版番号が正しくありません。'
}

# mainの初回pushには比較元のコミットがない。
$previousVersion = $null
if ($BeforeCommit -cne ('0' * 40)) {
    $previousPath = @(& git ls-tree --name-only $BeforeCommit -- $versionPath)
    if ($LASTEXITCODE -ne 0) {
        throw 'push前のコミットを取得できません。'
    }
    if ($previousPath.Count -gt 0) {
        $previous = @(& git show "${BeforeCommit}:$versionPath")
        if ($LASTEXITCODE -ne 0) {
            throw 'push前の版番号を取得できません。'
        }
        $previousVersion = ($previous -join "`n").Trim()
        if ($previousVersion -cnotmatch $versionPattern) {
            throw 'push前の版番号が正しくありません。'
        }
    }
}

[pscustomobject]@{
    Version = $version
    Changed = $version -cne $previousVersion
}
