# Downloads, verifies and runs blog-bootstrap on Windows.
#
# Environment:
#   BLOG_BOOTSTRAP_VERSION      tag such as v0.1.0 (default: latest)
#   BLOG_BOOTSTRAP_BASE_URL     repository base URL (for mirrors/tests)
#   BLOG_BOOTSTRAP_INSTALL_DIR  install directory (default: %LOCALAPPDATA%\Programs\blog-bootstrap)
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$BootstrapArgs
)

$ErrorActionPreference = "Stop"

$repo = $env:BLOG_BOOTSTRAP_BASE_URL
if ([string]::IsNullOrWhiteSpace($repo)) {
    $repo = "https://github.com/celestezj/blog-bootstrap"
}

switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture) {
    "X64" { $arch = "amd64" }
    "Arm64" { $arch = "arm64" }
    default { throw "unsupported architecture: $([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture)" }
}

$tag = $env:BLOG_BOOTSTRAP_VERSION
if ([string]::IsNullOrWhiteSpace($tag) -or $tag -eq "latest") {
    $release = Invoke-RestMethod -UseBasicParsing `
        -Uri "https://api.github.com/repos/celestezj/blog-bootstrap/releases/latest" `
        -Headers @{ "User-Agent" = "blog-bootstrap-install" }
    $tag = $release.tag_name
}
if ($tag -notmatch "^v") { $tag = "v$tag" }
$version = $tag.Substring(1)

$asset = "blog-bootstrap_${version}_windows_${arch}.zip"
$base = "$repo/releases/download/$tag"
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("blog-bootstrap-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    Write-Host "下载 $asset"
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile (Join-Path $tmp $asset)
    Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp "checksums.txt")

    $line = Get-Content (Join-Path $tmp "checksums.txt") |
        Where-Object { $_ -match "\s\*?$([regex]::Escape($asset))$" } |
        Select-Object -First 1
    if (-not $line) { throw "校验文件中缺少 $asset，已中止" }
    $want = ($line -split "\s+")[0]
    $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $asset)).Hash
    if ($got -ne $want) {
        throw "SHA256 校验失败: 期望 $want，实际 $got；已中止"
    }

    $out = Join-Path $tmp "out"
    Expand-Archive -Path (Join-Path $tmp $asset) -DestinationPath $out -Force
    $exe = Join-Path $out "blog-bootstrap.exe"

    $dest = $env:BLOG_BOOTSTRAP_INSTALL_DIR
    if ([string]::IsNullOrWhiteSpace($dest)) {
        $dest = Join-Path $env:LOCALAPPDATA "Programs\blog-bootstrap"
    }
    New-Item -ItemType Directory -Path $dest -Force | Out-Null
    $installed = Join-Path $dest "blog-bootstrap.exe"
    Copy-Item -Path $exe -Destination $installed -Force
    Write-Host "已安装: $installed"

    if ($BootstrapArgs.Count -gt 0) {
        & $installed @BootstrapArgs
    } else {
        & $installed version
    }
    exit $LASTEXITCODE
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
