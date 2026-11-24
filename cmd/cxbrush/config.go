package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// config 只保存上次登录的账号，不保存密码。
type config struct {
	Username string `json:"username"`
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "cxbrush", "config.json")
}

func loadConfig() config {
	var c config
	if p := configPath(); p != "" {
		if data, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(data, &c)
		}
	}
	return c
}

func saveConfig(c config) {
	p := configPath()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	_ = os.WriteFile(p, data, 0o600)
}
