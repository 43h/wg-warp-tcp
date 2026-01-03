@echo off
echo Building WireGuard TCP Proxy...

REM 创建输出目录
if not exist bin mkdir bin

REM 构建客户端
echo Building client...
set GOOS=linux
set GOARCH=amd64
go build -o bin\wg-tcp-client ./client
if %errorlevel% equ 0 (
    echo [OK] Client built successfully: bin\wg-tcp-client
) else (
    echo [FAIL] Client build failed
    exit /b 1
)

REM 构建服务端
echo Building server...
go build -o bin\wg-tcp-server ./server
if %errorlevel% equ 0 (
    echo [OK] Server built successfully: bin\wg-tcp-server
) else (
    echo [FAIL] Server build failed
    exit /b 1
)

echo.
echo Build completed successfully!
echo Client: bin\wg-tcp-client
echo Server: bin\wg-tcp-server

REM 构建 Windows 版本（可选）
echo.
echo Building Windows version...
set GOOS=windows
go build -o bin\wg-tcp-client.exe ./client
go build -o bin\wg-tcp-server.exe ./server

echo.
echo All builds completed!
