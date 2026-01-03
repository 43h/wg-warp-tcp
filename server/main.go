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
	UDP_READ_TIMEOUT                     = 1 * time.Second
)

type Server struct {
	config      *ServerConfig
	udpConn     *net.UDPConn
	listener    net.Listener
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	connManager *common.ConnectionManager
	indexToUUID sync.Map // sender_index -> uuid 映射
}

type ClientSession struct {
	uuid         string
	tcpConn      net.Conn
	udpToTcpChan chan []byte
	tcpToUdpChan chan []byte
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	senderIndex  uint32
	mu           sync.RWMutex
}

func NewServer(config *ServerConfig) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		config:      config,
		ctx:         ctx,
		cancel:      cancel,
		connManager: common.NewConnectionManager(),
	}
}

func (s *Server) Start() error {
	// 启动 UDP 连接到本地 WireGuard
	if err := s.startUDPConnection(); err != nil {
		return err
	}

	// 启动 TCP 监听
	if err := s.startTCPListener(); err != nil {
		return err
	}

	// 启动 UDP 接收循环
	s.wg.Add(1)
	go s.udpReceiveLoop()

	return nil
}

func (s *Server) startUDPConnection() error {
	localAddr := fmt.Sprintf("%s:%d", s.config.Local.IP, s.config.Local.Port)
	udpAddr, err := net.ResolveUDPAddr("udp", localAddr)
	if err != nil {
		common.LOGE("Failed to resolve UDP address:", err)
		return err
	}

	s.udpConn, err = net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		common.LOGE("Failed to connect UDP:", err)
		return err
	}

	common.LOGI("UDP connected to", localAddr)
	return nil
}

func (s *Server) startTCPListener() error {
	listenAddr := fmt.Sprintf("%s:%d", s.config.Listen.IP, s.config.Listen.Port)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		common.LOGE("Failed to listen TCP:", err)
		return err
	}

	s.listener = listener
	common.LOGI("TCP listening on", listenAddr)

	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				common.LOGE("Accept error:", err)
				continue
			}
		}

		common.LOGI("New TCP connection from", conn.RemoteAddr())
		s.wg.Add(1)
		go s.handleClient(conn)
	}
}

func (s *Server) handleClient(conn net.Conn) {
	defer s.wg.Done()

	uuid := fmt.Sprintf("%s-%d", conn.RemoteAddr().String(), time.Now().UnixNano())
	ctx, cancel := context.WithCancel(s.ctx)

	session := &ClientSession{
		uuid:         uuid,
		tcpConn:      conn,
		udpToTcpChan: make(chan []byte, 100),
		tcpToUdpChan: make(chan []byte, 100),
		ctx:          ctx,
		cancel:       cancel,
	}

	// 注册连接
	connInfo := &common.ConnInfo{
		UUID:       uuid,
		Conn:       conn,
		Status:     common.StatusConnected,
		MsgChannel: session.tcpToUdpChan,
		Timestamp:  time.Now().Unix(),
	}
	s.connManager.Add(uuid, connInfo)

	common.LOGI("Client session started:", uuid)

	// 启动收发协程
	session.wg.Add(2)
	go session.tcpSendLoop()
	go session.tcpReceiveLoop(s)

	// 等待会话结束
	session.wg.Wait()
	cancel()

	// 清理连接
	s.connManager.Delete(uuid)
	conn.Close()

	// 清理 sender_index 映射
	if session.senderIndex != 0 {
		s.indexToUUID.Delete(session.senderIndex)
	}

	common.LOGI("Client session ended:", uuid)
}

func (cs *ClientSession) tcpSendLoop() {
	defer cs.wg.Done()

	for {
		select {
		case <-cs.ctx.Done():
			return
		case data := <-cs.udpToTcpChan:
			if err := cs.sendToTCP(data); err != nil {
				common.LOGE("TCP send error:", err)
				cs.cancel()
				return
			}
			common.LOGD("Sent UDP packet to TCP, size:", len(data))
		}
	}
}

func (cs *ClientSession) tcpReceiveLoop(s *Server) {
	defer cs.wg.Done()

	buffer := make([]byte, 2048)
	lengthBuf := make([]byte, 2)

	for {
		select {
		case <-cs.ctx.Done():
			return
		default:
		}

		// 读取长度字段 (2 字节)
		cs.tcpConn.SetReadDeadline(time.Now().Add(30 * time.Second))
		if _, err := cs.tcpConn.Read(lengthBuf); err != nil {
			common.LOGE("TCP read length error:", err)
			cs.cancel()
			return
		}

		length := binary.BigEndian.Uint16(lengthBuf)
		if length > 2048 {
			common.LOGE("Invalid packet length:", length)
			cs.cancel()
			return
		}

		// 读取数据
		cs.tcpConn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n := 0
		for n < int(length) {
			nn, err := cs.tcpConn.Read(buffer[n:length])
			if err != nil {
				common.LOGE("TCP read data error:", err)
				cs.cancel()
				return
			}
			n += nn
		}

		// 解析并更新 sender_index
		if length >= 8 {
			messageType := buffer[0]
			if messageType == WG_MESSAGE_TYPE_HANDSHAKE_INITIATION {
				senderIndex := binary.LittleEndian.Uint32(buffer[SENDER_INDEX_OFFSET : SENDER_INDEX_OFFSET+4])
				cs.mu.Lock()
				oldIndex := cs.senderIndex
				cs.senderIndex = senderIndex
				cs.mu.Unlock()

				// 更新映射
				if oldIndex != 0 && oldIndex != senderIndex {
					s.indexToUUID.Delete(oldIndex)
				}
				s.indexToUUID.Store(senderIndex, cs.uuid)
				common.LOGI("Updated sender_index for", cs.uuid, ":", senderIndex)
			}
		}

		// 发送到本地 WireGuard
		if err := s.sendToUDP(buffer[:length]); err != nil {
			common.LOGE("UDP send error:", err)
		} else {
			common.LOGD("Sent TCP packet to WireGuard, size:", length)
		}
	}
}

func (cs *ClientSession) sendToTCP(data []byte) error {
	// 发送长度字段 (2 字节)
	lengthBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lengthBuf, uint16(len(data)))

	cs.tcpConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := cs.tcpConn.Write(lengthBuf); err != nil {
		return err
	}

	// 发送数据
	if _, err := cs.tcpConn.Write(data); err != nil {
		return err
	}

	return nil
}

func (s *Server) udpReceiveLoop() {
	defer s.wg.Done()

	buffer := make([]byte, 2048)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		s.udpConn.SetReadDeadline(time.Now().Add(UDP_READ_TIMEOUT))
		n, err := s.udpConn.Read(buffer)
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

		// 解析 sender_index，找到对应的 TCP 连接
		messageType := buffer[0]
		var targetUUID string

		if messageType == WG_MESSAGE_TYPE_HANDSHAKE_RESPONSE ||
			messageType == WG_MESSAGE_TYPE_TRANSPORT_DATA ||
			messageType == WG_MESSAGE_TYPE_COOKIE_REPLY {

			// 这些消息类型中的 receiver_index 字段在相同位置
			receiverIndex := binary.LittleEndian.Uint32(buffer[SENDER_INDEX_OFFSET : SENDER_INDEX_OFFSET+4])

			// 查找对应的 UUID
			if uuid, ok := s.indexToUUID.Load(receiverIndex); ok {
				targetUUID = uuid.(string)
			} else {
				common.LOGD("No session found for receiver_index:", receiverIndex)
				continue
			}
		} else {
			// 其他消息类型，广播给所有连接
			common.LOGD("Broadcasting message type:", messageType)
			s.broadcastToAllSessions(buffer[:n])
			continue
		}

		// 发送到对应的 TCP 连接
		if connInfo, exists := s.connManager.Get(targetUUID); exists {
			data := make([]byte, n)
			copy(data, buffer[:n])

			select {
			case connInfo.MsgChannel <- data:
				common.LOGD("UDP packet queued for session:", targetUUID, "size:", n)
			default:
				common.LOGD("Session channel full, dropping packet")
			}
		}
	}
}

func (s *Server) broadcastToAllSessions(data []byte) {
	s.connManager.ForEach(func(uuid string, info *common.ConnInfo) {
		dataCopy := make([]byte, len(data))
		copy(dataCopy, data)

		select {
		case info.MsgChannel <- dataCopy:
			common.LOGD("Broadcast packet to session:", uuid)
		default:
			common.LOGD("Session channel full, dropping broadcast packet:", uuid)
		}
	})
}

func (s *Server) sendToUDP(data []byte) error {
	s.udpConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := s.udpConn.Write(data)
	return err
}

func (s *Server) Stop() {
	common.LOGI("Stopping server...")
	s.cancel()

	if s.listener != nil {
		s.listener.Close()
	}

	if s.udpConn != nil {
		s.udpConn.Close()
	}

	s.wg.Wait()
	common.LOGI("Server stopped")
}

func main() {
	version := flag.Bool("v", false, "show version")
	debug := flag.Bool("d", false, "debug mode")
	configFile := flag.String("c", "server/conf.yaml", "config file path")
	flag.Parse()

	if *version {
		fmt.Println(common.ShowVersion())
		return
	}

	common.InitLog(*debug)
	defer common.CloseLog()

	common.LOGI("WireGuard TCP Proxy Server", common.ShowVersion())

	config, err := LoadServerConfig(*configFile)
	if err != nil || config == nil {
		common.LOGE("Failed to load config")
		os.Exit(1)
	}

	server := NewServer(config)
	if err := server.Start(); err != nil {
		common.LOGE("Failed to start server:", err)
		os.Exit(1)
	}

	// 等待退出信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	server.Stop()
}
