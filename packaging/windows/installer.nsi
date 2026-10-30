Unicode true

RequestExecutionLevel admin
SetCompressor /SOLID lzma

!define PRODUCT_NAME "Portico Media Server"
!define PRODUCT_PUBLISHER "Justin Ehler"
!define PRODUCT_WEB_SITE "https://getportico.tv"
!define SERVICE_NAME "PorticoMediaServer"

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

Section "Install"
  SetShellVarContext all
  ; Use the candidate's control command even when the installed binary predates SCM.
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File /oname=portico-service-control.exe "${STAGE_DIR}\portico-server.exe"
  nsExec::ExecToLog /TIMEOUT=60000 '"$PLUGINSDIR\portico-service-control.exe" --service-stop'
  Pop $2
  StrCmp $2 "0" service_stopped
  MessageBox MB_ICONSTOP "Portico could not stop. Installation files have been left in place." /SD IDOK
  SetErrorLevel 1
  Abort
service_stopped:
  ; Retire the old scheduled launch rather than keeping a second startup path.
  nsExec::ExecToLog /TIMEOUT=60000 'schtasks.exe /End /TN "Portico Media Server"'
  Pop $2
  nsExec::ExecToLog /TIMEOUT=60000 'schtasks.exe /Delete /TN "Portico Media Server" /F'
  Pop $2
  SetOutPath "$INSTDIR"
  File /r "${STAGE_DIR}\*"
  Delete "$INSTDIR\start-portico.cmd"
  Delete "$INSTDIR\startup-task.xml"
  WriteRegStr HKLM "Software\Portico Media Server" "InstallDir" "$INSTDIR"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Portico Media Server" "DisplayName" "${PRODUCT_NAME}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Portico Media Server" "DisplayVersion" "${PRODUCT_VERSION}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Portico Media Server" "Publisher" "${PRODUCT_PUBLISHER}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Portico Media Server" "URLInfoAbout" "${PRODUCT_WEB_SITE}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Portico Media Server" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  ReadEnvStr $0 "ProgramData"
  StrCmp $0 "" 0 +2
  StrCpy $0 "C:\ProgramData"
  StrCpy $0 "$0\Portico"
  CreateDirectory "$0"
  CreateDirectory "$0 Transcode"
  nsExec::ExecToLog /TIMEOUT=60000 '"$INSTDIR\portico-server.exe" --service-install'
  Pop $2
  StrCmp $2 "0" 0 install_failed
  nsExec::ExecToLog 'icacls.exe "$0" /inheritance:r /grant:r "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" "NT SERVICE\${SERVICE_NAME}:(OI)(CI)F"'
  Pop $2
  StrCmp $2 "0" 0 install_failed
  nsExec::ExecToLog 'icacls.exe "$0 Transcode" /inheritance:r /grant:r "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" "NT SERVICE\${SERVICE_NAME}:(OI)(CI)F"'
  Pop $2
  StrCmp $2 "0" 0 install_failed
  nsExec::ExecToLog 'netsh.exe advfirewall firewall delete rule name="Portico Media Server"'
  Pop $2
  nsExec::ExecToLog 'netsh.exe advfirewall firewall add rule name="Portico Media Server" dir=in action=allow program="$INSTDIR\portico-server.exe" enable=yes profile=any protocol=TCP localport=32500'
  Pop $2
  StrCmp $2 "0" 0 install_failed
  nsExec::ExecToLog /TIMEOUT=60000 '"$INSTDIR\portico-server.exe" --service-start'
  Pop $2
  StrCmp $2 "0" service_started
install_failed:
  MessageBox MB_ICONSTOP "Portico could not install or start its Windows service. Your server data has been kept." /SD IDOK
  SetErrorLevel 1
  Abort
service_started:
  CreateDirectory "$SMPROGRAMS\Portico Media Server"
  WriteINIStr "$SMPROGRAMS\Portico Media Server\Portico Media Server.url" "InternetShortcut" "URL" "http://127.0.0.1:32500/"
SectionEnd

Section "Uninstall"
  SetShellVarContext all
  nsExec::ExecToLog /TIMEOUT=60000 '"$INSTDIR\portico-server.exe" --service-uninstall'
  Pop $2
  StrCmp $2 "0" service_removed
  MessageBox MB_ICONSTOP "Portico could not remove its service. Installation files have been kept." /SD IDOK
  SetErrorLevel 1
  Abort
service_removed:
  nsExec::ExecToLog 'netsh.exe advfirewall firewall delete rule name="Portico Media Server"'
  Pop $2
  RMDir /r "$SMPROGRAMS\Portico Media Server"
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Portico Media Server"
  DeleteRegKey HKLM "Software\Portico Media Server"
  RMDir /r "$INSTDIR"
SectionEnd
