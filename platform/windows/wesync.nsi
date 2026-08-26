; WeSync Windows Installer
; Build: make windows  (cross-built via Docker; makensis is invoked by platform/linux/build.sh)

Unicode true

!define APP_NAME    "WeSync"
!define APP_VERSION "0.1.0"
!define INSTALL_DIR "$LOCALAPPDATA\WeSync"
!define REG_KEY     "Software\Microsoft\Windows\CurrentVersion\Uninstall\WeSync"
!define AUTORUN_KEY "Software\Microsoft\Windows\CurrentVersion\Run"

Name "${APP_NAME} ${APP_VERSION}"
OutFile "..\..\dist\windows\WeSync-${APP_VERSION}-setup.exe"
InstallDir "${INSTALL_DIR}"
InstallDirRegKey HKLM "${REG_KEY}" "InstallLocation"
; Per-user install — no admin required.
; Each user gets their own isolated WeSync in %LOCALAPPDATA%\WeSync directory.
RequestExecutionLevel user
SetCompressor /SOLID lzma

!include "MUI2.nsh"
!define MUI_ABORTWARNING
!define MUI_ICON "..\..\dist\windows\wesync.ico"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

; --------------------------------------------------------------------------
Section "WeSync" SecMain
  SectionIn RO

  ; 0. Stop running processes and WAIT until they have fully exited
  ; before attempting to overwrite the binary files.
  DeleteRegValue HKCU "${AUTORUN_KEY}" "WeSync"
  DeleteRegValue HKCU "${AUTORUN_KEY}" "WeSync App"
  ; wesync-app.exe supervises a child of the same name and restarts it when it
  ; dies unexpectedly (WebView2 crash recovery — see cmd/app/respawn_windows.go).
  ; Signal the shutdown event FIRST so the kill below reads as "stop", not
  ; "crash"; otherwise the supervisor could relaunch mid-upgrade and hold a lock
  ; on the .exe we are about to overwrite.
  nsExec::ExecToLog 'powershell -NoProfile -NonInteractive -Command "$$e = New-Object System.Threading.EventWaitHandle($$true, [System.Threading.EventResetMode]::ManualReset, \"Local\WeSyncAppShutdown\"); $$e.Set() | Out-Null"'
  nsExec::ExecToLog 'powershell -NoProfile -NonInteractive -Command "\"wesync\",\"wesync-app\" | ForEach-Object { Get-Process $$_ -ErrorAction SilentlyContinue | ForEach-Object { $$_.Kill(); $$_.WaitForExit(5000) } }"'
  nsExec::ExecToLog 'powershell -NoProfile -NonInteractive -Command "Get-Process syncthing -ErrorAction SilentlyContinue | Where-Object { $$_.Path -like \"*WeSync*\" } | ForEach-Object { $$_.Kill(); $$_.WaitForExit(3000) }"'

  ; 1. Install binaries and start an install log
  SetOutPath "$INSTDIR"
  File "..\..\wesync.exe"
  File "..\..\dist\windows\wesync-app.exe"
  CreateDirectory "$INSTDIR\data"
  FileOpen $R1 "$INSTDIR\data\install.log" a
  FileWrite $R1 "=== WeSync ${APP_VERSION} installed ===$\r$\n"
  FileClose $R1

  ; 2. Firewall rules — one single UAC prompt, waited synchronously.
  ;
  ; IMPORTANT: We use PowerShell Start-Process -Verb RunAs -Wait so the installer
  ; blocks until all rules are applied BEFORE WeSync starts listening (step 4).
  ; Without -Wait the rules arrive after WeSync is already up and Windows shows
  ; its own "allow/block" dialog even though we just added the rules.
  ;
  ; Rules:
  ;   WeSync Peer        TCP inbound to wesync.exe (peer wire, any port)
  ;   WeSync Discovery   UDP inbound to wesync.exe — covers WeSync's own multicast
  ;                      discovery (UDP 21026) and any future port. Program-scoped,
  ;                      NOT port 21027: that was a stale Syncthing port that never
  ;                      matched WeSync's listener, so inbound announces were blocked.
  ;   Syncthing Discovery UDP 21027 (Syncthing's own LAN discovery — a separate exe,
  ;                      so it needs a port rule, not the wesync.exe program rule)
  ;   WeSync Sync TCP    TCP 22000 (Syncthing BEP block-exchange protocol)
  ;   WeSync Sync QUIC   UDP 22000 (Syncthing QUIC transport, newer versions)
  DetailPrint "Configuring firewall (one admin prompt)..."
  FileOpen $R0 "$TEMP\wesync-fw.bat" w
  FileWrite $R0 "@echo off$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall delete rule name=$\"WeSync Peer$\"          >nul 2>nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall delete rule name=$\"WeSync Discovery$\"     >nul 2>nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall delete rule name=$\"Syncthing Discovery$\"  >nul 2>nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall delete rule name=$\"WeSync Sync TCP$\"      >nul 2>nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall delete rule name=$\"WeSync Sync QUIC$\"     >nul 2>nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall delete rule name=$\"WeSync Syncthing Sync$\" >nul 2>nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall add rule name=$\"WeSync Peer$\"        program=$\"$INSTDIR\wesync.exe$\" protocol=TCP  dir=in action=allow profile=any >nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall add rule name=$\"WeSync Discovery$\"   program=$\"$INSTDIR\wesync.exe$\" protocol=UDP  dir=in action=allow profile=any >nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall add rule name=$\"Syncthing Discovery$\" protocol=UDP  dir=in localport=21027 action=allow profile=any >nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall add rule name=$\"WeSync Sync TCP$\"    protocol=TCP  dir=in localport=22000 action=allow profile=any >nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall add rule name=$\"WeSync Sync QUIC$\"   protocol=UDP  dir=in localport=22000 action=allow profile=any >nul$\r$\n"
  FileClose $R0
  ; Start-Process -Verb RunAs -Wait: elevation + synchronous wait.
  ; PowerShell blocks until cmd.exe exits, so NSIS blocks until all rules are set.
  nsExec::ExecToLog 'powershell -NoProfile -NonInteractive -Command "Start-Process -FilePath cmd.exe -ArgumentList @(\"/C\",\"call\",\"$TEMP\wesync-fw.bat\") -Verb RunAs -Wait -WindowStyle Hidden"'
  Delete "$TEMP\wesync-fw.bat"

  ; 3. Register WeSync to start at login (visible in Task Manager > Startup)
  ;   WeSync     — the headless backend + Syncthing (the sync engine).
  ;   WeSync App — the tray/GUI. Launched with --hidden so login is SILENT:
  ;                it starts minimized to the tray (no window) but its webview
  ;                still connects, which gives the backend its foreground signal
  ;                so discovery + the peer wire come up. Without this entry the
  ;                tray never appears at login and discovery stays gated off.
  WriteRegStr HKCU "${AUTORUN_KEY}" "WeSync"     '"$INSTDIR\wesync.exe"'
  WriteRegStr HKCU "${AUTORUN_KEY}" "WeSync App" '"$INSTDIR\wesync-app.exe" --hidden'

  ; 4. Start WeSync now and open the UI
  ; WeSync manages Syncthing as a subprocess â€” no separate service needed.
  ; Data goes to %APPDATA%\WeSync\ which is naturally private to this user.
  DetailPrint "Starting WeSync..."
  Exec '"$INSTDIR\wesync.exe"'
  Exec '"$INSTDIR\wesync-app.exe"'

  ; 5. Shortcuts
  CreateDirectory "$SMPROGRAMS\WeSync"
  CreateShortcut "$SMPROGRAMS\WeSync\WeSync.lnk"           "$INSTDIR\wesync-app.exe"
  CreateShortcut "$SMPROGRAMS\WeSync\Uninstall WeSync.lnk"  "$INSTDIR\uninstall.exe"
  CreateShortcut "$DESKTOP\WeSync.lnk" "$INSTDIR\wesync-app.exe"

  ; 6. Add/Remove Programs
  WriteRegStr   HKCU "${REG_KEY}" "DisplayName"     "${APP_NAME}"
  WriteRegStr   HKCU "${REG_KEY}" "DisplayVersion"  "${APP_VERSION}"
  WriteRegStr   HKCU "${REG_KEY}" "Publisher"       "WeSync"
  WriteRegStr   HKCU "${REG_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr   HKCU "${REG_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${REG_KEY}" "NoModify"        1
  WriteRegDWORD HKCU "${REG_KEY}" "NoRepair"        1
  WriteUninstaller "$INSTDIR\uninstall.exe"
SectionEnd

; --------------------------------------------------------------------------
Section "Uninstall"
  DeleteRegValue HKCU "${AUTORUN_KEY}" "WeSync"
  DeleteRegValue HKCU "${AUTORUN_KEY}" "WeSync App"
  ; Tell the wesync-app supervisor this is a real shutdown before killing it,
  ; so it does not treat the kill as a crash and relaunch (see SecMain).
  nsExec::ExecToLog 'powershell -NoProfile -NonInteractive -Command "$$e = New-Object System.Threading.EventWaitHandle($$true, [System.Threading.EventResetMode]::ManualReset, \"Local\WeSyncAppShutdown\"); $$e.Set() | Out-Null"'
  ExecWait 'taskkill /F /IM "wesync.exe"' $0
  ExecWait 'taskkill /F /IM "wesync-app.exe"' $0
  ExecWait 'taskkill /F /IM "syncthing.exe"' $0
  Sleep 1500

  ; Remove application binaries.
  ; Your actual synced files are never touched.
  Delete "$INSTDIR\wesync.exe"
  Delete "$INSTDIR\wesync-app.exe"
  Delete "$INSTDIR\uninstall.exe"

  ; Ask whether to delete sync data (default = No = keep data).
  ; Keeping data means a reinstall picks up where you left off.
  MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 \
    "Delete your WeSync data?$\r$\n$\r$\nThis removes Syncthing configuration, device certificates, and sync history.$\r$\nYour actual synced files will NOT be deleted.$\r$\n$\r$\nClick No to keep your data (useful if you plan to reinstall)." \
    IDNO keep_data

  RMDir /r "$INSTDIR\data"
  RMDir /r "$LOCALAPPDATA\Syncthing"

  keep_data:
  ; Remove install dir only if now empty (silently skips if data\ still present).
  RMDir "$INSTDIR"

  Delete "$SMPROGRAMS\WeSync\WeSync.lnk"
  Delete "$SMPROGRAMS\WeSync\Uninstall WeSync.lnk"
  RMDir  "$SMPROGRAMS\WeSync"
  Delete "$DESKTOP\WeSync.lnk"

  DeleteRegKey HKCU "${REG_KEY}"

  ; Remove firewall rules added at install time.
  FileOpen $R0 "$TEMP\wesync-fw-remove.bat" w
  FileWrite $R0 "@echo off$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall delete rule name=$\"WeSync Peer$\"         >nul 2>nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall delete rule name=$\"WeSync Discovery$\"    >nul 2>nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall delete rule name=$\"Syncthing Discovery$\" >nul 2>nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall delete rule name=$\"WeSync Sync TCP$\"     >nul 2>nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall delete rule name=$\"WeSync Sync QUIC$\"    >nul 2>nul$\r$\n"
  FileWrite $R0 "netsh advfirewall firewall delete rule name=$\"WeSync Syncthing Sync$\" >nul 2>nul$\r$\n"
  FileClose $R0
  nsExec::ExecToLog 'powershell -NoProfile -NonInteractive -Command "Start-Process -FilePath cmd.exe -ArgumentList @(\"/C\",\"call\",\"$TEMP\wesync-fw-remove.bat\") -Verb RunAs -Wait -WindowStyle Hidden"'
  Delete "$TEMP\wesync-fw-remove.bat"
SectionEnd
