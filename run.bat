@echo off
REM WireGuard TCP Proxy - Quick Test Script for Windows

if "%1"=="" goto usage
if "%1"=="client" goto client
if "%1"=="server" goto server
if "%1"=="build" goto build
if "%1"=="test-client" goto test-client
if "%1"=="test-server" goto test-server
goto usage

:client
echo Starting TCP Proxy Client (Debug Mode)...
go run ./client -d -c client/conf.yaml
goto end

:server
echo Starting TCP Proxy Server (Debug Mode)...
go run ./server -d -c server/conf.yaml
goto end

:build
echo Building project...
call build.bat
goto end

:test-client
echo Testing client build...
go run ./client -v
goto end

:test-server
echo Testing server build...
go run ./server -v
goto end

:usage
echo Usage: %0 {client^|server^|build^|test-client^|test-server}
echo.
echo   client       - Run client in debug mode
echo   server       - Run server in debug mode
echo   build        - Build both client and server
echo   test-client  - Test client build and show version
echo   test-server  - Test server build and show version
goto end

:end
