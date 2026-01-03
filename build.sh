#!/bin/bash

echo "Building WireGuard TCP Proxy..."

# 构建客户端
echo "Building client..."
GOOS=linux GOARCH=amd64 go build -o bin/wg-tcp-client ./client
if [ $? -eq 0 ]; then
    echo "✓ Client built successfully: bin/wg-tcp-client"
else
    echo "✗ Client build failed"
    exit 1
fi

# 构建服务端
echo "Building server..."
GOOS=linux GOARCH=amd64 go build -o bin/wg-tcp-server ./server
if [ $? -eq 0 ]; then
    echo "✓ Server built successfully: bin/wg-tcp-server"
else
    echo "✗ Server build failed"
    exit 1
fi

echo ""
echo "Build completed successfully!"
echo "Client: bin/wg-tcp-client"
echo "Server: bin/wg-tcp-server"
