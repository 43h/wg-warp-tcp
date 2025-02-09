package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"strconv"
	"time"

	"gopkg.in/yaml.v2"
)

type Config struct {
	Local  Connection `yaml:"local"`
	Remote Connection `yaml:"remote"`
}

type Connection struct {
	IP   string `yaml:"ip"`
	Port int    `yaml:"port"`
}

var config Config

func loadConfig(filename string) error {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("error reading config file: %v", err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return fmt.Errorf("error unmarshalling config file: %v", err)
	}

	return nil
}

func dumpConfig() {
	fmt.Printf("Local IP: %s\n", config.Local.IP)
	fmt.Printf("Local Port: %d\n", config.Local.Port)
	fmt.Printf("Remote IP: %s\n", config.Remote.IP)
	fmt.Printf("Remote Port: %d\n", config.Remote.Port)
}

func udpServer(upstream chan<- []byte, downstream <-chan []byte) {
	addr := net.UDPAddr{
		Port: config.Local.Port,
		IP:   net.ParseIP(config.Local.IP),
	}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	// send
	go func() {
		for message := range downstream {
			_, err := conn.WriteToUDP(message, &addr)
			if err != nil {
				log.Printf("error: %v", err)
			}
		}
	}()

	// recv
	for {
		buffer := make([]byte, 2048)
		n, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			log.Printf("error: %v", err)
			continue
		}
		upstream <- buffer[:n]
	}
}

func tcpClient(upstream <-chan []byte, downstream chan<- []byte) {
	for {
		conn, err := net.Dial("tcp", config.Remote.IP+":"+strconv.Itoa(config.Remote.Port))
		if err != nil {
			log.Printf("error connecting to server, retrying: %v", err)
			time.Sleep(2 * time.Second)
		}

		//send
		go func() {
			for {
				lenBytes := make([]byte, 2)
				_, err := conn.Read(lenBytes)
				if err != nil {
					log.Printf("error reading message length: %v", err)
					break
				}
				length := int(lenBytes[0])<<8 + int(lenBytes[1])
				log.Printf("message length: %d", length)
				buffer := make([]byte, length)
				n, err := conn.Read(buffer)
				if err != nil {
					log.Printf("error reading from server: %v", err)
					break
				}
				downstream <- buffer[:n]
			}
		}()
		//recv
		for message := range upstream {
			length := len(message)
			lenBytes := []byte{byte(length >> 8), byte(length & 0xff)}
			_, err := conn.Write(lenBytes) // send message length
			if err == nil {
				_, err := conn.Write(message) // send message
				if err != nil {
					//Todo: reconnect
					log.Printf("error sending message: %v", err)
					break
				}
			} else {
				log.Printf("error sending message: %v", err)
				break
			}
		}
		err = conn.Close()
		if err != nil {
			log.Printf("error closing connection: %v", err)
		}
	}
}

func main() {
	err := loadConfig("conf.yaml")
	if err != nil {
		log.Fatalf("error: %v", err)
	} else {
		dumpConfig()
	}

	msgUpstream := make(chan []byte, 1024)
	msgDownstream := make(chan []byte, 1024)
	go udpServer(msgUpstream, msgDownstream)
	go tcpClient(msgUpstream, msgDownstream)
	for {
		time.Sleep(1000 * time.Second)
	}
}
