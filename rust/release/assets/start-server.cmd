@echo off
setlocal
if defined TAM_DATA_DIR goto run
if not defined LOCALAPPDATA (
    echo LOCALAPPDATA is unset; set TAM_DATA_DIR to a dedicated server folder. 1>&2
    exit /b 1
)
set "TAM_DATA_DIR=%LOCALAPPDATA%\R-TAM\server"
:run
"%~dp0r-tam-server.exe" %*
exit /b %errorlevel%
