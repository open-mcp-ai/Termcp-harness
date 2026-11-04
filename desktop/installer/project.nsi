Unicode true

!define PRODUCT_EXECUTABLE "Termcp Harness.exe"
!define HARNESS_PACKAGE_DIR "@@HARNESS_PACKAGE_DIR@@"
!include "wails_tools.nsh"
!include "nsDialogs.nsh"
!include "FileFunc.nsh"

Var PersistentCore
Var PersistentCoreCheckbox
Var ExistingProfile
Var CoreDataDir
Var CoreEndpoint
Var CoreProfile

VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_ABORTWARNING

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
Page custom PersistentCorePageCreate PersistentCorePageLeave
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\Harness_Install.exe"

!ifdef WAILS_INSTALL_SCOPE
  !if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
  !else
    InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
  !endif
!else
  InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
!endif

ShowInstDetails show

Function .onInit
  !insertmacro wails.checkArchitecture
  StrCpy $PersistentCore "0"
  StrCpy $ExistingProfile "0"
  StrCpy $CoreDataDir ""
  StrCpy $CoreEndpoint ""
  StrCpy $CoreProfile "$APPDATA\termcp-harness\core-service.json"
  IfFileExists "$INSTDIR\core-service-profile.txt" 0 profileOverride
    FileOpen $9 "$INSTDIR\core-service-profile.txt" r
    FileRead $9 $CoreProfile
    FileClose $9
  profileOverride:
  ${GetOptions} $CMDLINE "/CORE_PROFILE=" $0
  ${If} $0 != ""
    StrCpy $CoreProfile $0
  ${EndIf}
  IfFileExists "$CoreProfile" 0 checkSilent
    StrCpy $PersistentCore "1"
    StrCpy $ExistingProfile "1"
  checkSilent:
  ${GetOptions} $CMDLINE "/PERSISTENT_CORE=" $0
  ${If} $0 == "1"
    StrCpy $PersistentCore "1"
  ${ElseIf} $0 == "0"
    StrCpy $PersistentCore "0"
  ${EndIf}
  ${GetOptions} $CMDLINE "/CORE_DATA_DIR=" $CoreDataDir
  ${GetOptions} $CMDLINE "/CORE_ENDPOINT_PORT=" $CoreEndpoint
FunctionEnd

Function PersistentCorePageCreate
  !insertmacro MUI_HEADER_TEXT "Core 运行方式" "选择 Core 是否在 Harness 退出后继续运行"
  nsDialogs::Create 1018
  Pop $0
  ${If} $0 == error
    Abort
  ${EndIf}

  ${NSD_CreateLabel} 0 0 100% 42u "持久 Core 会以当前用户身份在后台运行，Harness 退出后仍保留 Session。关闭此选项时，Core 由 Harness 启动并在退出时停止。"
  Pop $0

  ${NSD_CreateCheckbox} 0 50u 100% 22u "启用持久 Core（独立运行，不随 Harness 退出而停止）"
  Pop $PersistentCoreCheckbox
  ${If} $ExistingProfile == "1"
    ${NSD_Check} $PersistentCoreCheckbox
    ${NSD_CreateLabel} 0 78u 100% 86u "检测到已有持久 Core profile。保持勾选会升级并继续使用该 profile；取消勾选会停止 Core，并把 profile 重命名为 .disabled 备份，Core 数据目录保留。升级期间已有 live SSH/PTY 会断开。"
    Pop $0
  ${Else}
    ${If} $PersistentCore == "1"
      ${NSD_Check} $PersistentCoreCheckbox
    ${EndIf}
  ${EndIf}

  nsDialogs::Show
FunctionEnd

Function PersistentCorePageLeave
  ${NSD_GetState} $PersistentCoreCheckbox $0
  ${If} $0 == ${BST_CHECKED}
    StrCpy $PersistentCore "1"
  ${Else}
    StrCpy $PersistentCore "0"
  ${EndIf}
FunctionEnd

Section
  !insertmacro wails.setShellContext
  !insertmacro wails.webview2runtime
  CreateDirectory $INSTDIR
  FileOpen $9 "$INSTDIR\installer.log" w
  FileWrite $9 "persistent=$PersistentCore existing=$ExistingProfile$\r$\n"
  FileWrite $9 "endpoint=$CoreEndpoint$\r$\ndata_dir=$CoreDataDir$\r$\nprofile=$CoreProfile$\r$\n"
  FileClose $9

  ${If} $ExistingProfile == "1"
    SetOutPath "$INSTDIR"
    IfFileExists "$INSTDIR\termcp-harness.exe" 0 profileStopDone
      nsExec::ExecToStack '"$INSTDIR\termcp-harness.exe" core service stop --profile "$CoreProfile"'
      Pop $0
      Pop $1
    profileStopDone:
  ${EndIf}

  SetOutPath $INSTDIR
  !insertmacro wails.files

  File "${HARNESS_PACKAGE_DIR}\termcp-harness.exe"
  File "${HARNESS_PACKAGE_DIR}\README.md"
  File "${HARNESS_PACKAGE_DIR}\CHANGELOG.md"
  File "${HARNESS_PACKAGE_DIR}\release.json"
  File "${HARNESS_PACKAGE_DIR}\checksums.txt"

  SetOutPath "$INSTDIR\core"
  File "${HARNESS_PACKAGE_DIR}\core\manifest.json"
  File "${HARNESS_PACKAGE_DIR}\core\termcp-core.exe"

  ${If} $PersistentCore == "1"
    ${If} $ExistingProfile == "1"
      StrCpy $2 '"$INSTDIR\termcp-harness.exe" core service install --profile "$CoreProfile"'
    ${Else}
      StrCpy $2 '"$INSTDIR\termcp-harness.exe" core service install --profile "$CoreProfile"'
      ${If} $CoreEndpoint != ""
        StrCpy $2 '$2 --endpoint "http://$CoreEndpoint"'
      ${EndIf}
      ${If} $CoreDataDir != ""
        StrCpy $2 '$2 --data-dir "$CoreDataDir"'
      ${EndIf}
    ${EndIf}
    FileOpen $9 "$INSTDIR\installer.log" a
    FileWrite $9 "core_install_command=$2$\r$\n"
    FileClose $9
    nsExec::ExecToStack '$2'
    Pop $0
    Pop $1
    FileOpen $9 "$INSTDIR\installer.log" a
    FileWrite $9 "core_install_exit=$0$\r$\ncore_install_output=$1$\r$\n"
    FileClose $9
    ${If} $0 != 0
      IfSilent silentProfileFailure
      MessageBox MB_ICONSTOP|MB_OK "持久 Core profile 配置失败。安装文件已复制，请运行 termcp-harness.exe core service install 查看错误。"
      silentProfileFailure:
      SetErrorLevel 1
    ${Else}
      nsExec::ExecToStack '"$INSTDIR\termcp-harness.exe" core service start --profile "$CoreProfile"'
      Pop $0
      Pop $1
      FileOpen $9 "$INSTDIR\installer.log" a
      FileWrite $9 "core_start_exit=$0$\r$\ncore_start_output=$1$\r$\n"
      FileClose $9
      ${If} $0 != 0
        IfSilent silentStartFailure
        MessageBox MB_ICONSTOP|MB_OK "持久 Core 启动失败。请运行 termcp-harness.exe core service status 检查状态。"
        silentStartFailure:
        SetErrorLevel 1
      ${EndIf}
    ${EndIf}
    FileOpen $9 "$INSTDIR\core-service-profile.txt" w
    FileWrite $9 "$CoreProfile"
    FileClose $9
  ${Else}
    ${If} $ExistingProfile == "1"
      System::Call 'kernel32::GetTickCount()i .r0'
      ClearErrors
      Rename "$CoreProfile" "$CoreProfile.disabled.$0"
      ${If} ${Errors}
        IfSilent silentDisableFailure
        MessageBox MB_ICONSTOP|MB_OK "无法停用持久 Core profile。原 profile 未被删除，请检查文件权限。"
        silentDisableFailure:
        SetErrorLevel 1
      ${EndIf}
    ${EndIf}
    Delete "$INSTDIR\core-service-profile.txt"
  ${EndIf}

  IfSilent noShortcuts
  CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
  CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
  noShortcuts:

  !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall"
  !insertmacro wails.setShellContext
  StrCpy $CoreProfile "$APPDATA\termcp-harness\core-service.json"
  IfFileExists "$INSTDIR\core-service-profile.txt" 0 uninstallProfileReady
    FileOpen $9 "$INSTDIR\core-service-profile.txt" r
    FileRead $9 $CoreProfile
    FileClose $9
  uninstallProfileReady:

  IfFileExists "$INSTDIR\termcp-harness.exe" 0 removeFiles
  IfFileExists "$CoreProfile" 0 removeFiles
    nsExec::ExecToStack '"$INSTDIR\termcp-harness.exe" core service stop --profile "$CoreProfile"'
    Pop $0
    Pop $1

  removeFiles:
  StrCpy $0 0
  removeRetry:
  RMDir /r $INSTDIR
  IfFileExists "$INSTDIR\*.*" 0 removeComplete
  Sleep 500
  IntOp $0 $0 + 1
  IntCmp $0 20 removeIncomplete removeRetry removeIncomplete
  removeIncomplete:
  IfSilent removeFinish
  MessageBox MB_ICONEXCLAMATION|MB_OK "安装目录仍有文件被占用，用户 Core 数据没有被删除。请稍后手动删除 $INSTDIR。"
  removeFinish:
  removeComplete:
  Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
  Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

  !insertmacro wails.deleteUninstaller
  RMDir "$INSTDIR"
SectionEnd
