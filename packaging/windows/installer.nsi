Unicode true

; Portico runs as the person who installs it, as Plex does: it starts when they sign in to
; Windows, as them, so it reads the media their own account can (their folders, and network
; shares they have signed in to), and keeps its data in their %LOCALAPPDATA%\Portico. The
; installer needs administrator rights only for Program Files and the firewall rule.
RequestExecutionLevel admin
SetCompressor /SOLID lzma

!define PRODUCT_NAME "Portico Media Server"
!define PRODUCT_PUBLISHER "Justin Ehler"
!define PRODUCT_WEB_SITE "https://getportico.tv"
!define RUN_KEY "Software\Microsoft\Windows\CurrentVersion\Run"
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Portico Media Server"

Name "${PRODUCT_NAME}"
Icon "portico.ico"
UninstallIcon "portico.ico"
OutFile "${OUTPUT_FILE}"
InstallDir "$PROGRAMFILES64\Portico Media Server"
InstallDirRegKey HKLM "Software\Portico Media Server" "InstallDir"

Page directory
Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

; Ends every running copy of the installed program, and waits until it has gone: Windows keeps
; a running program's files. It ends the way a sign-out from Windows ends it.
!macro StopPortico
  nsExec::ExecToLog `"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -Command "$$p = @(Get-Process portico-server -ErrorAction SilentlyContinue | Where-Object Path -eq '$INSTDIR\portico-server.exe'); $$p | Stop-Process -Force; $$p | Wait-Process -Timeout 30"`
  Pop $2
!macroend

Section "Install"
  SetShellVarContext current
  ; An install from before 18 Nov 2026 ran Portico as a Windows service: it is removed. Its data
  ; in %ProgramData%\Portico is left where it is.
  nsExec::ExecToLog 'sc.exe stop PorticoMediaServer'
  Pop $2
  nsExec::ExecToLog 'sc.exe delete PorticoMediaServer'
  Pop $2
  !insertmacro StopPortico
  StrCmp $2 "0" stopped
  MessageBox MB_ICONSTOP "Portico could not stop. Installation files have been left in place." /SD IDOK
  SetErrorLevel 1
  Abort
stopped:
  SetOutPath "$INSTDIR"
  File /r "${STAGE_DIR}\*"
  WriteRegStr HKLM "Software\Portico Media Server" "InstallDir" "$INSTDIR"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayName" "${PRODUCT_NAME}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayVersion" "${PRODUCT_VERSION}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "Publisher" "${PRODUCT_PUBLISHER}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "URLInfoAbout" "${PRODUCT_WEB_SITE}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  nsExec::ExecToLog 'netsh.exe advfirewall firewall delete rule name="Portico Media Server"'
  Pop $2
  nsExec::ExecToLog 'netsh.exe advfirewall firewall add rule name="Portico Media Server" dir=in action=allow program="$INSTDIR\portico-server.exe" enable=yes profile=any protocol=TCP localport=32500'
  Pop $2
  StrCmp $2 "0" firewall_set
  MessageBox MB_ICONSTOP "Portico could not add its firewall rule." /SD IDOK
  SetErrorLevel 1
  Abort
firewall_set:
  ; Started at each sign-in, as the person signing in. Settings > Apps > Startup can turn it off.
  WriteRegStr HKCU "${RUN_KEY}" "${PRODUCT_NAME}" '"$INSTDIR\portico-server.exe"'
  CreateDirectory "$SMPROGRAMS\Portico Media Server"
  WriteINIStr "$SMPROGRAMS\Portico Media Server\Portico Media Server.url" "InternetShortcut" "URL" "http://127.0.0.1:32500/"
  CreateShortcut "$SMPROGRAMS\Portico Media Server\Start Portico Media Server.lnk" "$INSTDIR\portico-server.exe"
  ; Started now as well, through Explorer: this installer runs with administrator rights, and
  ; Explorer starts the program with the owner's ordinary ones, as a sign-in does.
  Exec '"$WINDIR\explorer.exe" "$INSTDIR\portico-server.exe"'
SectionEnd

Section "Uninstall"
  SetShellVarContext current
  !insertmacro StopPortico
  StrCmp $2 "0" stopped
  MessageBox MB_ICONSTOP "Portico could not stop. Installation files have been kept." /SD IDOK
  SetErrorLevel 1
  Abort
stopped:
  DeleteRegValue HKCU "${RUN_KEY}" "${PRODUCT_NAME}"
  nsExec::ExecToLog 'netsh.exe advfirewall firewall delete rule name="Portico Media Server"'
  Pop $2
  RMDir /r "$SMPROGRAMS\Portico Media Server"
  DeleteRegKey HKLM "${UNINSTALL_KEY}"
  DeleteRegKey HKLM "Software\Portico Media Server"
  RMDir /r "$INSTDIR"
SectionEnd
