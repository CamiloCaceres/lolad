#!/bin/sh
# Builds a relocatable tmux from pinned source: libevent is linked statically,
# ncurses and libresolv are the ones every Mac ships in /usr/lib. The result
# therefore needs no install_name_tool work and runs on a Mac with no Homebrew.
#
# Usage: build-tmux.sh <outdir> [arch ...]      arch: arm64 x86_64 (default: both)
# Writes <outdir>/tmux (universal when both archs built) and <outdir>/tmux.version.
# An arch that cannot be built here (e.g. x86_64 without Rosetta) is reported
# and skipped; the exit status is 2 when fewer archs than requested were built.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=../versions.env disable=SC1091
. "$here/../versions.env"
TMUX_URL="https://github.com/tmux/tmux/releases/download/$TMUX_VERSION/tmux-$TMUX_VERSION.tar.gz"
LIBEVENT_URL="https://github.com/libevent/libevent/releases/download/release-$LIBEVENT_VERSION/libevent-$LIBEVENT_VERSION.tar.gz"
MIN_MACOS=13.0

out=${1:?usage: build-tmux.sh <outdir> [arch ...]}
shift
archs=${*:-arm64 x86_64}
mkdir -p "$out"
out=$(cd "$out" && pwd)
work="${TMUX_WORK:-$out/work}"
mkdir -p "$work"
jobs=$(sysctl -n hw.ncpu 2>/dev/null || echo 4)

fetch() { # url sha256 file
  if [ ! -f "$work/$3" ]; then
    curl -fsSL --retry 3 -o "$work/$3.part" "$1"
    mv "$work/$3.part" "$work/$3"
  fi
  got=$(shasum -a 256 "$work/$3" | cut -d' ' -f1)
  if [ "$got" != "$2" ]; then
    echo "build-tmux: checksum mismatch for $3: got $got, want $2" >&2
    rm -f "$work/$3"
    exit 1
  fi
}
fetch "$TMUX_URL" "$TMUX_SHA256" "tmux-$TMUX_VERSION.tar.gz"
fetch "$LIBEVENT_URL" "$LIBEVENT_SHA256" "libevent-$LIBEVENT_VERSION.tar.gz"

built=""
for arch in $archs; do
  case "$arch" in
    arm64) host=aarch64-apple-darwin ;;
    x86_64) host=x86_64-apple-darwin ;;
    *) echo "build-tmux: unknown arch $arch" >&2; exit 1 ;;
  esac
  # A cross-arch binary has to run for configure's probes (Rosetta on Apple Silicon).
  if ! printf 'int main(void){return 0;}' | clang -arch "$arch" -x c - -o "$work/probe-$arch" 2>/dev/null \
     || ! arch -"$arch" "$work/probe-$arch" 2>/dev/null; then
    echo "build-tmux: cannot build or run $arch binaries on this machine; skipping $arch (build it on a $arch runner)" >&2
    continue
  fi
  prefix="$work/prefix-$arch"
  src="$work/src-$arch"
  rm -rf "$prefix" "$src"
  mkdir -p "$src"
  tar -xzf "$work/libevent-$LIBEVENT_VERSION.tar.gz" -C "$src"
  tar -xzf "$work/tmux-$TMUX_VERSION.tar.gz" -C "$src"
  export CC="clang -arch $arch -mmacosx-version-min=$MIN_MACOS"
  export CFLAGS="-O2 -mmacosx-version-min=$MIN_MACOS"
  export LDFLAGS="-mmacosx-version-min=$MIN_MACOS"
  echo "build-tmux: libevent ($arch)"
  (cd "$src/libevent-$LIBEVENT_VERSION" &&
    ./configure --host="$host" --prefix="$prefix" --disable-shared --enable-static \
      --disable-openssl --disable-samples --disable-libevent-regress >"$work/libevent-$arch.log" 2>&1 &&
    make -j"$jobs" >>"$work/libevent-$arch.log" 2>&1 && make install >>"$work/libevent-$arch.log" 2>&1) ||
    { tail -20 "$work/libevent-$arch.log" >&2; exit 1; }
  echo "build-tmux: tmux ($arch)"
  # Plain `ncurses` resolves to /usr/lib/libncurses.dylib (system, always present).
  (cd "$src/tmux-$TMUX_VERSION" &&
    PKG_CONFIG=false \
    LIBEVENT_CFLAGS="-I$prefix/include" LIBEVENT_LIBS="$prefix/lib/libevent_core.a" \
    LIBNCURSES_CFLAGS="" LIBNCURSES_LIBS="-lncurses" \
    CPPFLAGS="-I$prefix/include" \
    ./configure --host="$host" --disable-utf8proc >"$work/tmux-$arch.log" 2>&1 &&
    make -j"$jobs" >>"$work/tmux-$arch.log" 2>&1) || { tail -30 "$work/tmux-$arch.log" >&2; exit 1; }
  strip -x "$src/tmux-$TMUX_VERSION/tmux"
  cp "$src/tmux-$TMUX_VERSION/tmux" "$work/tmux-$arch"
  built="$built $arch"
done

[ -n "$built" ] || { echo "build-tmux: no architecture could be built" >&2; exit 2; }
set --
for a in $built; do set -- "$@" "$work/tmux-$a"; done
if [ $# -gt 1 ]; then lipo -create "$@" -output "$out/tmux"; else cp "$1" "$out/tmux"; fi
chmod 755 "$out/tmux"
echo "$TMUX_VERSION" >"$out/tmux.version"
echo "build-tmux: wrote $out/tmux ($(lipo -archs "$out/tmux"))"
want=$(echo "$archs" | wc -w)
have=$(echo "$built" | wc -w)
[ "$want" -eq "$have" ] || exit 2
