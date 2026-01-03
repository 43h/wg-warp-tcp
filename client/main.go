package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"main/common"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const (
	WG_MESSAGE_TYPE_HANDSHAKE_INITIATION = 1
	WG_MESSAGE_TYPE_HANDSHAKE_RESPONSE   = 2
	WG_MESSAGE_TYPE_COOKIE_REPLY         = 3
	WG_MESSAGE_TYPE_TRANSPORT_DATA       = 4
	SENDER_INDEX_OFFSET                  = 4
	RECONNECT_INTERVAL                   = 3 * time.Second
	UDP_READ_TIMEOUT                     = 1 * time.Second
)

type Client struct {
	config        *ClientConfig
	udpConn       *net.UDPConn
	tcpConn       net.Conn
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.RWMutex
	tcpConnected  bool
	udpToTcpChan  chan []byte
	tcpToUdpChan  chan []byte
	wg            sync.WaitGroup
	senderIndex   uint32
	remoteUDPAddr *net.UDPAddr
}

func NewClient(config *ClientConfig) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		config:       config,
		ctx:          ctx,
		cancel:       cancel,
		udpToTcpChan: make(chan []byte, 100),
		tcpToUdpChan: make(chan []byte, 100),
	}
}

func (c *Client) Start() error {
	// 启动 UDP 监听
	if err := c.startUDPListener(); err != nil {
		return err
	}

	// 启动 TCP 连接管理
	c.wg.Add(1)
	go c.manageTCPConnection()

	return nil
}

func (c *Client) startUDPListener() error {
	localAddr := fmt.Sprintf("%s:%d", c.config.Local.IP, c.config.Local.Port)
	udpAddr, err := net.ResolveUDPAddr("udp", localAddr)
	if err != nil {
		common.LOGE("Failed to resolve UDP address:", err)
		return err
	}

	c.udpConn, err = net.ListenUDP("udp", udpAddr)
	if err != nil {
		common.LOGE("Failed to listen UDP:", err)
		return err
	}

	common.LOGI("UDP listening on", localAddr)

	c.wg.Add(1)
	go c.udpReceiveLoop()

	return nil
}

func (c *Client) udpReceiveLoop() {
	defer c.wg.Done()

	buffer := make([]byte, 2048)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.udpConn.SetReadDeadline(time.Now().Add(UDP_READ_TIMEOUT))
		n, remoteAddr, err := c.udpConn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			common.LOGE("UDP read error:", err)
			continue
		}

		if n < 4 {
			common.LOGD("UDP packet too short:", n)
			continue
		}

		// 记录远端 UDP 地址
		if c.remoteUDPAddr == nil || c.remoteUDPAddr.String() != remoteAddr.String() {
			c.remoteUDPAddr = remoteAddr
			common.LOGI("WireGuard peer address:", remoteAddr)
		}

		// 解析 sender_index
		messageType := buffer[0]
		if messageType == WG_MESSAGE_TYPE_HANDSHAKE_INITIATION {
			senderIndex := binary.LittleEndian.Uint32(buffer[SENDER_INDEX_OFFSET : SENDER_INDEX_OFFSET+4])
			c.mu.Lock()
			if c.senderIndex != senderIndex {
				c.senderIndex = senderIndex
				common.LOGI("Updated sender_index:", senderIndex)
			}
			c.mu.Unlock()
		}

		// 只有在 TCP 连接建立时才转发 UDP 报文
		c.mu.RLock()
		connected := c.tcpConnected
		c.mu.RUnlock()

		if connected {
			// 复制数据到新的 buffer
			data := make([]byte, n)
			copy(data, buffer[:n])

			select {
			case c.udpToTcpChan <- data:
				common.LOGD("UDP packet queued, size:", n)
			default:
				common.LOGD("UDP to TCP channel full, dropping packet")
			}
		} else {
			common.LOGD("TCP not connected, dropping UDP packet")
		}
	}
}

func (c *Client) manageTCPConnection() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		common.LOGI("Connecting to TCP server...")
		if err := c.connectTCP(); err != nil {
			common.LOGE("TCP connection failed:", err)
			time.Sleep(RECONNECT_INTERVAL)
			continue
		}

		common.LOGI("TCP connected successfully")
		c.mu.Lock()
		c.tcpConnected = true
		c.mu.Unlock()

		// 启动 TCP 收发协程
		var tcpWg sync.WaitGroup
		tcpCtx, tcpCancel := context.WithCancel(c.ctx)

		tcpWg.Add(2)
		go c.tcpSendLoop(tcpCtx, &tcpWg)
		go c.tcpReceiveLoop(tcpCtx, &tcpWg)

		// 等待 TCP 连接断开
		tcpWg.Wait()
		tcpCancel()

		c.mu.Lock()
		c.tcpConnected = false
		if c.tcpConn != nil {
			c.tcpConn.Close()
			c.tcpConn = nil
		}
		c.mu.Unlock()

		common.LOGE("TCP connection closed, reconnecting...")
		time.Sleep(RECONNECT_INTERVAL)
	}
}

func (c *Client) connectTCP() error {
	remoteAddr := fmt.Sprintf("%s:%d", c.config.Remote.IP, c.config.Remote.Port)
	conn, err := net.DialTimeout("tcp", remoteAddr, 10*time.Second)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.tcpConn = conn
	c.mu.Unlock()

	return nil
}

func (c *Client) tcpSendLoop(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case data := <-c.udpToTcpChan:
			if err := c.sendToTCP(data); err != nil {
				common.LOGE("TCP send error:", err)
				return
			}
			common.LOGD("Sent UDP packet to TCP, size:", len(data))
		}
	}
}

func (c *Client) tcpReceiveLoop(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	buffer := make([]byte, 2048)
	lengthBuf := make([]byte, 2)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		c.mu.RLock()
		conn := c.tcpConn
		c.mu.RUnlock()

		if conn == nil {
			return
		}

		// 读取长度字段 (2 字节)
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		if _, err := conn.Read(lengthBuf); err != nil {
			common.LOGE("TCP read length error:", err)
			return
		}

		length := binary.BigEndian.Uint16(lengthBuf)
		if length > 2048 {
			common.LOGE("Invalid packet length:", length)
			return
		}

		// 读取数据
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n := 0
		for n < int(length) {
			nn, err := conn.Read(buffer[n:length])
			if err != nil {
				common.LOGE("TCP read data error:", err)
				return
			}
			n += nn
		}

		// 发送到 UDP
		if err := c.sendToUDP(buffer[:length]); err != nil {
			common.LOGE("UDP send error:", err)
		} else {
			common.LOGD("Sent TCP packet to UDP, size:", length)
		}
	}
}

func (c *Client) sendToTCP(data []byte) error {
	c.mu.RLock()
	conn := c.tcpConn
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("TCP connection not established")
	}

	// 发送长度字段 (2 字节)
	lengthBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lengthBuf, uint16(len(data)))

	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(lengthBuf); err != nil {
		return err
	}

	// 发送数据
	if _, err := conn.Write(data); err != nil {
		return err
	}

	return nil
}

func (c *Client) sendToUDP(data []byte) error {
	if c.remoteUDPAddr == nil {
		return fmt.Errorf("remote UDP address not known")
	}

	c.udpConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := c.udpConn.WriteToUDP(data, c.remoteUDPAddr)
	return err
}

func (c *Client) Stop() {
	common.LOGI("Stopping client...")
	c.cancel()

	c.mu.Lock()
	if c.tcpConn != nil {
		c.tcpConn.Close()
		c.tcpConn = nil
	}
	if c.udpConn != nil {
		c.udpConn.Close()
		c.udpConn = nil
	}
	c.mu.Unlock()

	c.wg.Wait()
	common.LOGI("Client stopped")
}

func main() {
	version := flag.Bool("v", false, "show version")
	debug := flag.Bool("d", false, "debug mode")
	configFile := flag.String("c", "client/conf.yaml", "config file path")
	flag.Parse()

	if *version {
		fmt.Println(common.ShowVersion())
		return
	}

	common.InitLog(*debug)
	defer common.CloseLog()

	common.LOGI("WireGuard TCP Proxy Client", common.ShowVersion())

	config, err := LoadClientConfig(*configFile)
	if err != nil || config == nil {
		common.LOGE("Failed to load config")
		os.Exit(1)
	}

	client := NewClient(config)
	if err := client.Start(); err != nil {
		common.LOGE("Failed to start client:", err)
		os.Exit(1)
	}

	// 等待退出信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	client.Stop()
}
