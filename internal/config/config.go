package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"ticket-auction-manager/tam-go/internal/env"
)

var Path string = env.GetConfigPath()

func ReadConfigFile() ConfigFile {
	defaultFile, err := json.MarshalIndent(DefaultConfigFile, "", "  ")
	if err != nil {
		panic(err)
	}
	if _, err := os.Stat(Path); errors.Is(err, os.ErrNotExist) {
		os.WriteFile(Path, defaultFile, 0644)
	} else if err != nil {
		panic(err)
	}
	fileData, err := os.ReadFile(Path)
	if err != nil {
		panic(err)
	}
	var rtnConf ConfigFile
	err = json.Unmarshal(fileData, &rtnConf)
	if err != nil {
		panic(err)
	}
	return rtnConf
}

func WriteConfigFile(nf ConfigFile) {
	encFile, err := json.MarshalIndent(nf, "", "  ")
	if err != nil {
		panic(err)
	}
	os.WriteFile(Path, encFile, 0644)
}

func GetAllSettings(w http.ResponseWriter, r *http.Request) {
	fileData := ReadConfigFile()

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(fileData)
}

func GetRemoteURL(s ConfigFile) string {
	var (
		httpstr   string
		serverstr string
		port      string
	)
	if s.RemoteServer != "" {
		switch s.RemoteTLS {
		case true:
			httpstr = "https"
		case false:
			httpstr = "http"
		}
		serverstr = s.RemoteServer
		port = s.RemotePort
		NewURL := fmt.Sprintf("%s://%s:%s/", httpstr, serverstr, port)
		return NewURL
	} else {
		return ""
	}
}

func SaveAllSettings(w http.ResponseWriter, r *http.Request) {
	var newConfig ConfigFile
	json.NewDecoder(r.Body).Decode(&newConfig)

	WriteConfigFile(newConfig)
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(newConfig)
}

type ConfigFile struct {
	RemoteServer  string `json:"remote_server"`
	RemoteKey     string `json:"remote_key"`
	RemotePort    string `json:"remote_port"`
	RemoteTLS     bool   `json:"remote_tls"`
	DefaultPref   string `json:"default_pref"`
	VenueName     string `json:"venue_name"`
	DisableAttrib bool   `json:"disable_attrib"`
}

var DefaultConfigFile ConfigFile = ConfigFile{
	RemoteServer:  "",
	RemoteKey:     "",
	RemotePort:    "8000",
	RemoteTLS:     false,
	DefaultPref:   "CALL",
	VenueName:     "Test Venue",
	DisableAttrib: false,
}
