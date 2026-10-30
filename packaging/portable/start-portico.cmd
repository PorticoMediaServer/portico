@echo off
setlocal
if not defined PORTICO_WEB_DIR set "PORTICO_WEB_DIR=%~dp0web"
"%~dp0portico-server.exe" %*
exit /b %errorlevel%
