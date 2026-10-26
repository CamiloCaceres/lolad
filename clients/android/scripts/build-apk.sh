#!/bin/zsh
set -euo pipefail
cd "${0:A:h:h}"
export JAVA_HOME="${JAVA_HOME:-/Users/you/android/jdk/Contents/Home}"
export ANDROID_HOME="${ANDROID_HOME:-/Users/you/android/sdk}"
export GRADLE_USER_HOME="${GRADLE_USER_HOME:-/Users/you/android/gradle-home}"
signing_dir="${LOLAD_ANDROID_SIGNING_DIR:-$HOME/.local/share/lolad-android}"
mkdir -p "$signing_dir" dist
chmod 700 "$signing_dir"
if [[ ! -f "$signing_dir/release.p12" ]]; then
 /usr/bin/python3 - "$signing_dir/password" <<'PY'
import secrets,sys,os
fd=os.open(sys.argv[1],os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
with os.fdopen(fd,'w') as f:f.write(secrets.token_hex(32)+'\n')
PY
 "$JAVA_HOME/bin/keytool" -genkeypair -keystore "$signing_dir/release.p12" -storetype PKCS12 -alias lolad -keyalg RSA -keysize 3072 -validity 10000 -dname 'CN=Milo, OU=lolad, O=Personal' -storepass:file "$signing_dir/password" -keypass:file "$signing_dir/password"
 chmod 600 "$signing_dir/release.p12"
fi
./gradlew testDebugUnitTest lintRelease assembleRelease --console=plain
build_tools="$ANDROID_HOME/build-tools/35.0.0"
"$build_tools/zipalign" -f -p 4 app/build/outputs/apk/release/app-release-unsigned.apk dist/lolad.apk
"$build_tools/apksigner" sign --ks "$signing_dir/release.p12" --ks-key-alias lolad --ks-pass "file:$signing_dir/password" dist/lolad.apk
"$build_tools/apksigner" verify --verbose dist/lolad.apk
(cd dist && shasum -a 256 lolad.apk > SHA256SUMS)
