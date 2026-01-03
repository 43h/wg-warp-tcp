package main

import (
	"main/common"

	"gopkg.in/yaml.v3"
)

type Address struct {
	IP   string `yaml:"ip"`
	Port int    `yaml:"port"`
}

type ServerConfig struct {
	Local  Address `yaml:"local"`
	Listen Address `yaml:"listen"`
}

func LoadServerConfig(configFile string) (*ServerConfig, error) {
	data := common.LoadConf(configFile)
	if data == nil {
		return nil, nil
	}

	var config ServerConfig
	err := yaml.Unmarshal(data, &config)
	if err != nil {
		common.LOGE("fail to unmarshal config:", err)
		return nil, err
	}

	common.LOGI("Server config loaded - Local:", config.Local.IP, ":", config.Local.Port,
		"Listen:", config.Listen.IP, ":", config.Listen.Port)

	return &config, nil
}
