#!/usr/bin/env sh
# Downloads, verifies and runs blog-bootstrap on macOS/Linux.
#
# Environment:
#   BLOG_BOOTSTRAP_VERSION      tag such as v0.1.0 (default: latest)
#   BLOG_BOOTSTRAP_BASE_URL     repository base URL (for mirrors/tests)
#   BLOG_BOOTSTRAP_INSTALL_DIR  install directory (default: ~/.local/bin)
set -eu

repo="${BLOG_BOOTSTRAP_BASE_URL:-https://github.com/celestezj/blog-bootstrap}"

os="$(uname -s)"
case "$os" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) echo "unsupported OS: $os (仅支持 macOS/Linux)" >&2; exit 1 ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

tag="${BLOG_BOOTSTRAP_VERSION:-}"
if [ -z "$tag" ] || [ "$tag" = "latest" ]; then
  location="$(curl -fsSI "$repo/releases/latest" | tr -d '\r' | awk 'tolower($1)=="location:" {print $2}' | tail -n 1)"
  tag="${location##*/}"
  if [ -z "$tag" ]; then
    echo "无法解析最新版本；请设置 BLOG_BOOTSTRAP_VERSION=vX.Y.Z" >&2
    exit 1
  fi
fi
case "$tag" in
  v*) ;;
  *) tag="v$tag" ;;
esac
version="${tag#v}"

asset="blog-bootstrap_${version}_${os}_${arch}.tar.gz"
base="$repo/releases/download/$tag"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "下载 $asset" >&2
curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

want="$(awk -v name="$asset" '$2 == name || $2 == "*" name {print $1}' "$tmp/checksums.txt" | head -n 1)"
if [ -z "$want" ]; then
  echo "校验文件中缺少 $asset，已中止" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
else
  got="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
fi
if [ "$got" != "$want" ]; then
  echo "SHA256 校验失败: 期望 $want，实际 $got；已中止" >&2
  exit 1
fi

mkdir -p "$tmp/out"
tar -xzf "$tmp/$asset" -C "$tmp/out" blog-bootstrap

dest="${BLOG_BOOTSTRAP_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$dest"
cp "$tmp/out/blog-bootstrap" "$dest/blog-bootstrap"
chmod 0755 "$dest/blog-bootstrap"
echo "已安装: $dest/blog-bootstrap" >&2

if [ "$#" -gt 0 ]; then
  exec "$dest/blog-bootstrap" "$@"
fi
exec "$dest/blog-bootstrap" version
