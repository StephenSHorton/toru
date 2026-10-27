Unicode true

####
## Please note: Template replacements don't work in this file. They are provided with default defines like
## mentioned underneath.
## If the keyword is not defined, "wails_tools.nsh" will populate them.
## If they are defined here, "wails_tools.nsh" will not touch them. This allows you to use this project.nsi manually
## from outside of Wails for debugging and development of the installer.
## 
## For development first make a wails nsis build to populate the "wails_tools.nsh":
## > wails build --target windows/amd64 --nsis
## Then you can call makensis on this file with specifying the path to your binary:
## For a AMD64 only installer:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app.exe
## For a ARM64 only installer:
## > makensis -DARG_WAILS_ARM64_BINARY=..\..\bin\app.exe
## For a installer with both architectures:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app-amd64.exe -DARG_WAILS_ARM64_BINARY=..\..\bin\app-arm64.exe
####
## The following information is taken from the wails_tools.nsh file, but they can be overwritten here.
####
## !define INFO_PROJECTNAME    "my-project" # Default "toru"
## !define INFO_COMPANYNAME    "My Company" # Default "StephenSHorton"
## !define INFO_PRODUCTNAME    "My Product Name" # Default "Toru"
## !define INFO_PRODUCTVERSION "1.0.0"     # Default "0.1.0"
## !define INFO_COPYRIGHT      "(c) Now, My Company" # Default "© 2026, My Company"
###
## !define PRODUCT_EXECUTABLE  "Application.exe"      # Default "${INFO_PROJECTNAME}.exe"
## !define UNINST_KEY_NAME     "UninstKeyInRegistry"  # Default "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
####
## !define REQUEST_EXECUTION_LEVEL "admin"            # Default "admin"  see also https://nsis.sourceforge.io/Docs/Chapter4.html
## !define WAILS_INSTALL_SCOPE     "user"             # Default "machine" - set to "user" for per-user install ($LOCALAPPDATA) without UAC prompt
####
## Publisher and copyright: wails_tools.nsh still carries the Wails placeholders
## ("© 2026, My Company"), so set the real ones here. They show in the
## installer's file properties and in Apps & features.
!ifndef INFO_COMPANYNAME
    !define INFO_COMPANYNAME "Stephen Horton"
!endif
!ifndef INFO_COPYRIGHT
    # ASCII only: this file has no BOM, so makensis reads it as ANSI and a
    # UTF-8 "©" would come out as "Â©".
    !define INFO_COPYRIGHT "Copyright (c) 2026 Stephen Horton. MIT License."
!endif
## The uninstall registry key was named from the old company name. Keep it, or
## every existing install's next silent update would write a second Apps &
## features entry and orphan the first.
!ifndef UNINST_KEY_NAME
    !define UNINST_KEY_NAME "StephenSHortonToru"
!endif
####
## LAN payload (docs/lan.md, "Elevation, the service and installer changes").
## toru-lan.exe, wintun.dll and Wintun's license are installed next to toru.exe
## in the per-user folder. Nothing here registers the service: Toru runs
## "toru-lan.exe install" with one UAC prompt the first time LAN is turned on,
## and that copies the files to Program Files. The release passes
## -DARG_TORU_LAN_BINARY=...\bin\toru-lan.exe; without it the installer is
## built without LAN. The Wintun files and the notices default to the repo.
!ifndef ARG_WINTUN_DLL
    !define ARG_WINTUN_DLL "..\wintun\wintun.dll"
!endif
!ifndef ARG_WINTUN_LICENSE
    !define ARG_WINTUN_LICENSE "..\wintun\LICENSE.txt"
!endif
!ifndef ARG_THIRD_PARTY_NOTICES
    !define ARG_THIRD_PARTY_NOTICES "..\..\..\THIRD_PARTY_NOTICES"
!endif
!define TORU_LAN_EXE "toru-lan.exe"
!define TORU_LAN_SERVICE "ToruLan" # internal/lan/api ServiceName
!define TORU_LAN_SERVICE_KEY "SYSTEM\CurrentControlSet\Services\${TORU_LAN_SERVICE}"
####
## Include the wails tools
####
!include "wails_tools.nsh"

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

# Offer to launch Toru when an INTERACTIVE install finishes (checkbox, checked by
# default). The per-user installer is non-elevated, so this runs Toru at the
# normal user level. Silent (auto-update) installs skip the Finish page and
# relaunch via the Exec at the end of the install Section instead.
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"
!define MUI_FINISHPAGE_RUN_TEXT "Launch ${INFO_PRODUCTNAME}"

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uninstalling page

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe" # Name of the installer's file.
!if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
!else
    InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
!endif
ShowInstDetails show # This will always show the installation details.

# "1" on a first-time install, "0" on an update/reinstall (toru.exe already
# present). Gates the Desktop-shortcut creation so an update never resurrects a
# shortcut the user deleted. Set inside the install Section before files land.
Var FreshInstall

Function .onInit
   !insertmacro wails.checkArchitecture
FunctionEnd

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    # AUTO-UPDATE: a SILENT install is launched by the running Toru, which then
    # quits to release its lock on toru.exe. Wait for that exit so wails.files
    # can overwrite the exe AND so the old instance's single-instance mutex is
    # freed before we relaunch below. A normal interactive install is never
    # locked, so this falls straight through.
    #
    # The probe opens the exe for writing (append, no truncation), which is the
    # same thing File will do. It must NOT be a rename: Windows lets a running
    # executable be renamed, so a rename probe passed while the old Toru was
    # still shutting down, File then failed (silent mode ignores that), the
    # uninstaller was written for a binary we never replaced, and the relaunch
    # handed off to the dying instance and exited. Result: a stale Toru that
    # re-ran the update on every launch and "closed every time it opened".
    #
    # Shutdown can take a few seconds (background recording stops ffmpeg), so
    # allow up to 60 s. If the exe is still locked after that, abort and change
    # nothing: the running Toru retries the update at its next start.
    ${If} ${Silent}
        StrCpy $0 0
        toru_wait_unlock:
            IfFileExists "$INSTDIR\${PRODUCT_EXECUTABLE}" 0 toru_unlocked
            ClearErrors
            FileOpen $1 "$INSTDIR\${PRODUCT_EXECUTABLE}" a
            IfErrors toru_still_locked
            FileClose $1
            Goto toru_unlocked
            toru_still_locked:
            IntOp $0 $0 + 1
            IntCmp $0 300 toru_gave_up 0 toru_gave_up
            Sleep 200
            Goto toru_wait_unlock
        toru_gave_up:
            SetErrorLevel 3
            Abort "toru.exe is still in use; update skipped"
        toru_unlocked:
    ${EndIf}

    # Detect FRESH install vs UPDATE *before* laying down files: if toru.exe is
    # already present, this is an update/reinstall. On an update we must NOT
    # recreate the Desktop shortcut (the user may have deleted it on purpose).
    StrCpy $FreshInstall "1"
    ${If} ${FileExists} "$INSTDIR\${PRODUCT_EXECUTABLE}"
        StrCpy $FreshInstall "0"
    ${EndIf}

    SetOutPath $INSTDIR

    !insertmacro wails.files

    File "/oname=THIRD_PARTY_NOTICES.txt" "${ARG_THIRD_PARTY_NOTICES}"

    # LAN payload. The Program Files copy the service runs from is separate, so
    # a silent update never touches a running service; Toru offers "Update LAN
    # (needs admin)" when the two differ.
    !ifdef ARG_TORU_LAN_BINARY
        ${If} ${IsNativeAMD64}
            File "/oname=${TORU_LAN_EXE}" "${ARG_TORU_LAN_BINARY}"
            File "/oname=wintun.dll" "${ARG_WINTUN_DLL}"
            File "/oname=wintun-LICENSE.txt" "${ARG_WINTUN_LICENSE}"
        ${EndIf}
    !else
        !warning "ARG_TORU_LAN_BINARY not set: building the installer without LAN (toru-lan.exe, wintun.dll)"
    !endif

    # Start-Menu shortcut is always (re)created; Desktop shortcut only on a fresh
    # install so updates don't keep resurrecting a shortcut the user removed.
    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    ${If} $FreshInstall == "1"
        CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    ${EndIf}

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller

    # AUTO-UPDATE: after a silent update, relaunch the freshly-installed Toru so
    # the app comes back on its own -- UNLESS the updater passed /NORELAUNCH.
    # The running Toru launches us two ways (internal/update):
    #   "Restart now"  -> toru.exe /S              (user wants Toru back: relaunch)
    #   quit with an   -> toru.exe /S /NORELAUNCH  (user chose to quit; installing
    #   update ready                                 silently must not bring it back)
    # Per-user / non-elevated installer, so Toru starts at the normal user level.
    # ${GetParameters}/${GetOptions} come from FileFunc.nsh (wails_tools.nsh).
    ${If} ${Silent}
        ${GetParameters} $R0
        ClearErrors
        ${GetOptions} $R0 "/NORELAUNCH" $R1
        ${If} ${Errors}
            # Flag absent: a "Restart now" update, bring Toru back.
            Exec '"$INSTDIR\${PRODUCT_EXECUTABLE}"'
        ${EndIf}
        # Flag present: install-on-exit, leave Toru closed.
    ${EndIf}
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    # LAN: the ToruLan service, its adapter, firewall rules and the Program
    # Files copy are machine-wide, which this per-user uninstaller can't remove
    # by itself. If the service exists, run "toru-lan.exe uninstall" elevated
    # (one UAC prompt) before $INSTDIR, and the exe in it, are deleted. If the
    # prompt is refused, everything else is still uninstalled and we say what
    # was left behind. Users can read the Services key, so the check itself
    # needs no elevation.
    SetRegView 64
    ClearErrors
    ReadRegStr $0 HKLM "${TORU_LAN_SERVICE_KEY}" "ImagePath"
    ${IfNot} ${Errors}
        StrCpy $1 "$INSTDIR\${TORU_LAN_EXE}"
        ${IfNot} ${FileExists} "$1"
            StrCpy $1 "$PROGRAMFILES64\Toru\lan\${TORU_LAN_EXE}"
        ${EndIf}
        DetailPrint "Removing Toru LAN (needs administrator approval)..."
        ClearErrors
        ExecShellWait "runas" "$1" "uninstall" SW_HIDE
        StrCpy $2 "refused" # ShellExecuteEx failed: UAC refused, or no exe
        ${IfNot} ${Errors}
            ClearErrors
            ReadRegStr $0 HKLM "${TORU_LAN_SERVICE_KEY}" "ImagePath"
            StrCpy $2 "ok"
            ${IfNot} ${Errors}
                StrCpy $2 "failed" # ran, but the service is still registered
            ${EndIf}
        ${EndIf}
        ${If} $2 == "ok"
            DetailPrint "Toru LAN removed."
        ${Else}
            DetailPrint "Toru LAN was not removed ($2)."
            ${IfNot} ${Silent}
                MessageBox MB_OK|MB_ICONEXCLAMATION "Toru LAN (the ToruLan service, the Toru LAN network adapter and its firewall rules) is still installed, because administrator approval was refused or the removal failed.$\r$\n$\r$\nTo remove it, run this as administrator:$\r$\n$\"$PROGRAMFILES64\Toru\lan\${TORU_LAN_EXE}$\" uninstall"
            ${EndIf}
        ${EndIf}
    ${EndIf}

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath

    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd
