package main

import (
	"main/common"

	"gopkg.in/yaml.v3"
)

type Address struct {
	IP   string `yaml:"ip"`
	Port int    `yaml:"port"`
}

type ClientConfig struct {
	Local  Address `yaml:"local"`
	Remote Address `yaml:"remote"`
}

func LoadClientConfig(configFile string) (*ClientConfig, error) {
	data := common.LoadConf(configFile)
	if data == nil {
		return nil, nil
	}

	var config ClientConfig
	err := yaml.Unmarshal(data, &config)
	if err != nil {
		common.LOGE("fail to unmarshal config:", err)
		return nil, err
	}

	common.LOGI("Client config loaded - Local:", config.Local.IP, ":", config.Local.Port,
		"Remote:", config.Remote.IP, ":", config.Remote.Port)

	return &config, nil
}
