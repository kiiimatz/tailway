@echo off
setlocal

echo Building tailway...

if not exist dist (
    mkdir dist
)

echo [1/5] Linux amd64
set GOOS=linux
set GOARCH=amd64
go build -ldflags="-s -w" -o dist/tailway-linux-amd64 ./cmd/tailway
if errorlevel 1 goto error

echo [2/5] Linux arm64
set GOOS=linux
set GOARCH=arm64
go build -ldflags="-s -w" -o dist/tailway-linux-arm64 ./cmd/tailway
if errorlevel 1 goto error

echo [3/5] macOS amd64
set GOOS=darwin
set GOARCH=amd64
go build -ldflags="-s -w" -o dist/tailway-darwin-amd64 ./cmd/tailway
if errorlevel 1 goto error

echo [4/5] macOS arm64
set GOOS=darwin
set GOARCH=arm64
go build -ldflags="-s -w" -o dist/tailway-darwin-arm64 ./cmd/tailway
if errorlevel 1 goto error

echo [5/5] Windows amd64
set GOOS=windows
set GOARCH=amd64
go build -ldflags="-s -w" -o dist/tailway-windows-amd64.exe ./cmd/tailway
if errorlevel 1 goto error

echo.
echo Build completed!
goto end

:error
echo.
echo Build failed!
exit /b 1

:end
endlocal
pause
