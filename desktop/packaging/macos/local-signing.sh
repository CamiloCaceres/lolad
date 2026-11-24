# shellcheck shell=sh
# Sourced by bundle.sh and bin/release-local.sh. Finds the self-signed
# "Lolad Local Signing" identity (docs/desktop.md section 4, Signing).
#
# Why: an ad-hoc signature is a hash of the binary, so every build is a new
# program to macOS privacy controls (TCC), and the "may Lolad use the drive"
# permission is asked again after each update. A certificate signature with a
# fixed identifier has a designated requirement of
#   identifier "dev.caceres.lolad" and certificate root = H"<cert hash>"
# which is the same for every build signed with the same certificate, so a
# permission granted once survives updates. It does not satisfy Gatekeeper:
# that needs a Developer ID.
#
# The identity lives in its own keychain, unlocked with a password file, so a
# build never shows a keychain prompt:
#   LOLAD_SIGN_KEYCHAIN  default ~/Library/Keychains/lolad-signing.keychain-db
#   LOLAD_SIGN_DIR       default ~/.local/share/lolad-macos-signing
#                        (keychain.password, chmod 600; never printed)
#   LOLAD_SIGN_NAME      default "Lolad Local Signing"

# lolad_local_identity prints the identity's SHA-1, or nothing when this
# machine does not have it. It unlocks the keychain and adds it to the user's
# keychain search list when missing (codesign only finds identities there).
lolad_local_identity() {
  _kc=${LOLAD_SIGN_KEYCHAIN:-$HOME/Library/Keychains/lolad-signing.keychain-db}
  _dir=${LOLAD_SIGN_DIR:-$HOME/.local/share/lolad-macos-signing}
  _name=${LOLAD_SIGN_NAME:-Lolad Local Signing}
  [ -f "$_kc" ] || return 0
  command -v security >/dev/null || return 0
  if [ -r "$_dir/keychain.password" ]; then
    security unlock-keychain -p "$(cat "$_dir/keychain.password")" "$_kc" >/dev/null 2>&1 || return 0
  fi
  if ! security list-keychains -d user | grep -qF "\"$_kc\""; then
    # Keep every keychain already listed; add ours at the end.
    eval "set -- $(security list-keychains -d user | tr '\n' ' ')"
    security list-keychains -d user -s "$@" "$_kc"
  fi
  security find-identity -p codesigning "$_kc" 2>/dev/null |
    awk -v n="\"$_name\"" 'index($0, n) && $2 ~ /^[0-9A-F]{40}$/ { print $2; exit }'
}

# The fixed code-signing identifier for each Mach-O in Lolad.app. The bundle
# and its main executable use CFBundleIdentifier; the daemon uses the launchd
# label; anything else gets dev.caceres.lolad.<file name>.
lolad_sign_identifier() { # path app-path bundle-id
  case "$1" in
    "$2" | "$2/Contents/MacOS/"*) echo "$3" ;;
    "$2/Contents/Helpers/lolad") echo "dev.caceres.lolad" ;;
    *) echo "dev.caceres.lolad.$(basename "$1" | sed 's/\.dylib$//; s/[^A-Za-z0-9.-]/-/g')" ;;
  esac
}
