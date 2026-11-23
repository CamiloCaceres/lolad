# Adds or removes one directory in the per-user Path (HKCU\Environment).
# Run by the installer and uninstaller (installer.nsi). It replaces NSIS's
# ReadRegStr, which fails silently on values longer than NSIS_MAX_STRLEN (1024
# characters in the stock build), and would then have overwritten or deleted
# the user's whole Path.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File path.ps1 add|remove <dir>
#
# The value is read without expanding %VARIABLES% and written back with its
# original kind, so entries such as %USERPROFILE%\AppData\Local\... keep working.
# Any error exits non-zero before anything is written.
param(
  [Parameter(Mandatory = $true)][ValidateSet('add', 'remove')][string]$Action,
  [Parameter(Mandatory = $true)][string]$Dir
)
$ErrorActionPreference = 'Stop'

$key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
if ($null -eq $key) { $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment') }
try {
  $exists = $key.GetValueNames() -contains 'Path'
  $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
  $cur = ''
  if ($exists) {
    $kind = $key.GetValueKind('Path')
    $cur = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
  }
  $norm = { param($p) $p.Trim().TrimEnd('\').ToLowerInvariant() }
  $want = & $norm $Dir
  $parts = @($cur -split ';' | Where-Object { $_.Trim() -ne '' })
  $has = @($parts | Where-Object { (& $norm $_) -eq $want }).Count -gt 0

  if ($Action -eq 'add') {
    if ($has) { exit 0 }
    $parts += $Dir
  } else {
    if (-not $has) { exit 0 }
    $parts = @($parts | Where-Object { (& $norm $_) -ne $want })
  }

  if ($parts.Count -eq 0) {
    if ($exists) { $key.DeleteValue('Path', $false) }
  } else {
    $key.SetValue('Path', ($parts -join ';'), $kind)
  }
} finally {
  $key.Close()
}
exit 0
