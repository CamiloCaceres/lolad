; Lolad per-user installer (docs/desktop.md sections 4, 5, 11).
;
;   makensis /DVERSION=1.2.3 /DSTAGE=<dir> /DOUTFILE=Lolad-Setup.exe installer.nsi
;
; STAGE is the directory build.sh assembles:
;   Lolad.exe  bin\lolad.exe  bin\lolad-daemon.exe  bin\psmux\{psmux.exe,LICENSE}
;   lolabot\   multiplexer.version  LICENSE
; Optional: /DSIGN_PS1=<path to a script that signs the file given as $1>
;   (used to sign the uninstaller and the installer itself).
;
; No UAC: everything lives under HKCU and %LOCALAPPDATA%.
; /S installs silently (the updater uses it): no pages, no prompts, and the
; running app and daemon are stopped and restarted.

Unicode true
!ifndef VERSION
  !error "pass /DVERSION=x.y.z"
!endif
!ifndef STAGE
  !error "pass /DSTAGE=<staging dir>"
!endif
!ifndef OUTFILE
  !define OUTFILE "Lolad-Setup.exe"
!endif

!define APPNAME "Lolad"
!define PUBLISHER "Camilo Caceres"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Lolad"
!define RUN_KEY "Software\Microsoft\Windows\CurrentVersion\Run"
; Registry value names are case-insensitive: "lolad" and "Lolad" would be ONE value.
!define RUN_DAEMON "lolad"
!define RUN_APP "LoladApp"

Name "${APPNAME}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\Lolad"
InstallDirRegKey HKCU "${UNINST_KEY}" "InstallLocation"
RequestExecutionLevel user
SetCompressor /SOLID lzma
ShowInstDetails nevershow
ShowUninstDetails nevershow
BrandingText "Lolad ${VERSION}"

VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "Lolad"
VIAddVersionKey "CompanyName" "${PUBLISHER}"
VIAddVersionKey "FileDescription" "Lolad installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "MIT License"

!include MUI2.nsh
!include LogicLib.nsh
!include FileFunc.nsh
!include nsDialogs.nsh
!include StrFunc.nsh
!insertmacro GetSize
${StrRep}
${UnStrRep}

!ifdef SIGN_PS1
  !finalize 'powershell -NoProfile -ExecutionPolicy Bypass -File "${SIGN_PS1}" "%1"'
  !uninstfinalize 'powershell -NoProfile -ExecutionPolicy Bypass -File "${SIGN_PS1}" "%1"'
!endif

!define MUI_ABORTWARNING
!if /FileExists "${STAGE}\Lolad.ico"
  !define MUI_ICON "${STAGE}\Lolad.ico"
  !define MUI_UNICON "${STAGE}\Lolad.ico"
!endif

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\Lolad.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open Lolad now"
!insertmacro MUI_PAGE_FINISH

UninstPage custom un.OptionsCreate un.OptionsLeave
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Var UnDeleteData
Var UnCheckbox

; ---------------------------------------------------------------- helpers

; Stops the app and the daemon (both images). /T takes the watchdog's child too.
; Agents live in the psmux server, which is a separate process and keeps running.
!macro StopLolad PREFIX
  Function ${PREFIX}StopLolad
    nsExec::Exec 'taskkill /IM Lolad.exe /T /F'
    Pop $0
    nsExec::Exec 'taskkill /IM lolad-daemon.exe /T /F'
    Pop $0
    Sleep 1000
  FunctionEnd
!macroend
!insertmacro StopLolad ""
!insertmacro StopLolad "un."

; Tells running programs that HKCU\Environment changed (new terminals see Path).
!macro BroadcastEnv PREFIX
  Function ${PREFIX}BroadcastEnv
    System::Call 'user32::SendMessageTimeout(p 0xffff, i 0x1A, p 0, t "Environment", i 2, i 5000, *p .r0)'
  FunctionEnd
!macroend
!insertmacro BroadcastEnv ""
!insertmacro BroadcastEnv "un."

Function AddToPath
  ReadRegStr $0 HKCU "Environment" "Path"
  ; already there?
  ${StrRep} $1 "$0;" "$INSTDIR\bin;" ""
  ${If} $1 != "$0;"
    Return
  ${EndIf}
  ${If} $0 == ""
    StrCpy $0 "$INSTDIR\bin"
  ${Else}
    StrCpy $0 "$0;$INSTDIR\bin"
  ${EndIf}
  WriteRegExpandStr HKCU "Environment" "Path" "$0"
  Call BroadcastEnv
FunctionEnd

Function un.RemoveFromPath
  ReadRegStr $0 HKCU "Environment" "Path"
  ${UnStrRep} $0 "$0" ";$INSTDIR\bin" ""
  ${UnStrRep} $0 "$0" "$INSTDIR\bin;" ""
  ${UnStrRep} $0 "$0" "$INSTDIR\bin" ""
  ${If} $0 == ""
    DeleteRegValue HKCU "Environment" "Path"
  ${Else}
    WriteRegExpandStr HKCU "Environment" "Path" "$0"
  ${EndIf}
  Call un.BroadcastEnv
FunctionEnd

; Runs `psmux -L lolad kill-server` (all agents stop) when the multiplexer
; is installed. Errors are ignored.
!macro KillAgents PREFIX
  Function ${PREFIX}KillAgents
    ${If} ${FileExists} "$INSTDIR\bin\psmux\psmux.exe"
      nsExec::Exec '"$INSTDIR\bin\psmux\psmux.exe" -L lolad kill-server'
      Pop $0
      Sleep 1500
    ${EndIf}
  FunctionEnd
!macroend
!insertmacro KillAgents ""
!insertmacro KillAgents "un."

; ----------------------------------------------------------------- install

Section "Lolad" SecMain
  SectionIn RO
  SetShellVarContext current
  Call StopLolad

  ; Is the multiplexer changing? Then a running psmux server would hold the old
  ; psmux.exe open and the new client could not talk to it anyway.
  StrCpy $2 ""
  ${If} ${FileExists} "$INSTDIR\multiplexer.version"
    FileOpen $3 "$INSTDIR\multiplexer.version" r
    FileRead $3 $2
    FileClose $3
  ${EndIf}
  FileOpen $3 "${STAGE}\multiplexer.version" r
  FileRead $3 $4
  FileClose $3
  ${If} $2 != $4
    Call KillAgents
  ${EndIf}

  SetOutPath "$INSTDIR"
  SetOverwrite on
  File "${STAGE}\Lolad.exe"
  File "${STAGE}\multiplexer.version"
  File /nonfatal "${STAGE}\LICENSE"
  File /nonfatal "${STAGE}\NOTICE.md"
  File /nonfatal "${STAGE}\Lolad.ico"

  SetOutPath "$INSTDIR\bin"
  File "${STAGE}\bin\lolad.exe"
  File "${STAGE}\bin\lolad-daemon.exe"

  SetOutPath "$INSTDIR\bin\psmux"
  SetOverwrite try      ; a busy psmux.exe must not fail the whole install
  File "${STAGE}\bin\psmux\psmux.exe"
  File "${STAGE}\bin\psmux\LICENSE"
  SetOverwrite on

  ; lolabot: replace the whole tree so removed files do not linger.
  RMDir /r "$INSTDIR\lolabot"
  SetOutPath "$INSTDIR\lolabot"
  File /r "${STAGE}\lolabot\*.*"

  SetOutPath "$INSTDIR"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; Path (per user)
  Call AddToPath

  ; Start at login. Fresh install: both on (the app can switch them off).
  ; Update: keep the user's choice, only refresh the command if the value exists.
  ReadRegStr $5 HKCU "${UNINST_KEY}" "DisplayVersion"
  ReadRegStr $6 HKCU "${RUN_KEY}" "${RUN_DAEMON}"
  ReadRegStr $7 HKCU "${RUN_KEY}" "${RUN_APP}"
  ${If} $5 == ""
  ${OrIf} $6 != ""
    WriteRegStr HKCU "${RUN_KEY}" "${RUN_DAEMON}" '"$INSTDIR\bin\lolad-daemon.exe" serve --supervised'
  ${EndIf}
  ${If} $5 == ""
  ${OrIf} $7 != ""
    WriteRegStr HKCU "${RUN_KEY}" "${RUN_APP}" '"$INSTDIR\Lolad.exe" --background'
  ${EndIf}

  ; Start Menu
  CreateShortCut "$SMPROGRAMS\Lolad.lnk" "$INSTDIR\Lolad.exe" "" "$INSTDIR\Lolad.exe" 0

  ; Settings > Apps
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${APPNAME}"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\Lolad.exe"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" "$0"

  ; Silent update (the app launched us): bring the app back, tray only. The
  ; app starts the daemon itself with a sanitized environment; the installer
  ; must not, because it inherits the updater's environment (tokens).
  ${If} ${Silent}
    Exec '"$INSTDIR\Lolad.exe" --background'
  ${EndIf}
SectionEnd

Function .onInit
  ; one installer at a time
  System::Call 'kernel32::CreateMutex(p 0, i 1, t "LoladSetupMutex") p .r1 ?e'
  Pop $R0
  ${If} $R0 = 183 ; ERROR_ALREADY_EXISTS
    ${IfNot} ${Silent}
      MessageBox MB_OK|MB_ICONEXCLAMATION "The Lolad installer is already running."
    ${EndIf}
    Abort
  ${EndIf}
FunctionEnd

; --------------------------------------------------------------- uninstall

Function un.OptionsCreate
  nsDialogs::Create 1018
  Pop $0
  ${If} $0 == error
    Abort
  ${EndIf}
  ${NSD_CreateLabel} 0 0 100% 36u "Lolad will be removed from this PC. Your agents, mail and settings are kept so you can install Lolad again later.$\r$\n$\r$\nIf you want a clean slate, tick the box below."
  Pop $0
  ${NSD_CreateCheckbox} 0 50u 100% 12u "Also delete my agents and data"
  Pop $UnCheckbox
  ${NSD_CreateLabel} 12u 66u 95% 36u "Deletes the folders ~\.config\lolad and ~\.local\share\lolad. Your agents folder (~\Lolad Agents) is never deleted."
  Pop $0
  nsDialogs::Show
FunctionEnd

Function un.OptionsLeave
  ${NSD_GetState} $UnCheckbox $UnDeleteData
  ${If} $UnDeleteData == ${BST_CHECKED}
    MessageBox MB_YESNO|MB_ICONEXCLAMATION|MB_DEFBUTTON2 "This permanently deletes Lolad's settings, mail, logs and agent records on this PC. It cannot be undone.$\r$\n$\r$\nDelete them?" IDYES +2
    Abort
  ${EndIf}
FunctionEnd

Section "Uninstall"
  SetShellVarContext current
  Call un.StopLolad

  ${If} ${FileExists} "$INSTDIR\bin\psmux\psmux.exe"
    ${IfNot} ${Silent}
      MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 "Also stop all running agents?$\r$\n$\r$\nYes: agents stop now.$\r$\nNo: they keep running until you restart the PC or stop them yourself." IDYES stopyes IDNO stopno
      stopyes:
        Call un.KillAgents
      stopno:
    ${EndIf}
  ${EndIf}

  DeleteRegValue HKCU "${RUN_KEY}" "${RUN_DAEMON}"
  DeleteRegValue HKCU "${RUN_KEY}" "${RUN_APP}"
  Call un.RemoveFromPath
  DeleteRegKey HKCU "${UNINST_KEY}"
  Delete "$SMPROGRAMS\Lolad.lnk"

  ; Program files. /REBOOTOK handles a psmux.exe that is still serving agents.
  Delete /REBOOTOK "$INSTDIR\Lolad.exe"
  Delete /REBOOTOK "$INSTDIR\bin\lolad.exe"
  Delete /REBOOTOK "$INSTDIR\bin\lolad-daemon.exe"
  Delete /REBOOTOK "$INSTDIR\bin\psmux\psmux.exe"
  Delete /REBOOTOK "$INSTDIR\bin\psmux\LICENSE"
  RMDir "$INSTDIR\bin\psmux"
  RMDir "$INSTDIR\bin"
  RMDir /r /REBOOTOK "$INSTDIR\lolabot"
  Delete "$INSTDIR\multiplexer.version"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\NOTICE.md"
  Delete "$INSTDIR\Lolad.ico"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"

  ${If} $UnDeleteData == ${BST_CHECKED}
    RMDir /r "$PROFILE\.config\lolad"
    RMDir /r "$PROFILE\.local\share\lolad"
  ${EndIf}
SectionEnd
