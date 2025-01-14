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

func readConfig(filename string) error {
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

func udpServer(messages chan<- []byte) {
	addr := net.UDPAddr{
		Port: config.Local.Port,
		IP:   net.ParseIP(config.Local.IP),
	}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	for {
		buffer := make([]byte, 2048)
		n, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			log.Printf("error: %v", err)
			continue
		}
		messages <- buffer[:n]
	}
}

func tcpClient(messages <-chan []byte) {
	for {
		conn, err := net.Dial("tcp", config.Remote.IP+":"+strconv.Itoa(config.Remote.Port))
		if err != nil {
			log.Printf("error connecting to server, retrying: %v", err)
			time.Sleep(2 * time.Second)
		}

		for message := range messages {
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
	err := readConfig("conf.yaml")
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	messages := make(chan []byte, 1024)
	go udpServer(messages)
	go tcpClient(messages)
	for {
		time.Sleep(1000 * time.Second)
	}
}
