#!/bin/bash

# WireGuard TCP Proxy - 快速测试脚本

case "$1" in
    client)
        echo "Starting TCP Proxy Client (Debug Mode)..."
        go run ./client -d -c client/conf.yaml
        ;;
    server)
        echo "Starting TCP Proxy Server (Debug Mode)..."
        go run ./server -d -c server/conf.yaml
        ;;
    build)
        echo "Building project..."
        ./build.sh
        ;;
    test-client)
        echo "Testing client build..."
        go run ./client -v
        ;;
    test-server)
        echo "Testing server build..."
        go run ./server -v
        ;;
    *)
        echo "Usage: $0 {client|server|build|test-client|test-server}"
        echo ""
        echo "  client       - Run client in debug mode"
        echo "  server       - Run server in debug mode"
        echo "  build        - Build both client and server"
        echo "  test-client  - Test client build and show version"
        echo "  test-server  - Test server build and show version"
        exit 1
        ;;
esac
