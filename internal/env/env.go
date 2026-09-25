package env

import (
	"os"
	"path"
)

func GetDataDir() string {
	val, exists := os.LookupEnv("TAM_DATA_DIR")
	if !exists {
		val = path.Clean("./data")
	} else {
		val = path.Clean(val)
	}
	os.MkdirAll(val, 0755)
	return val
}

func GetDBPath() string {
	base_dir := GetDataDir()
	var FileName string
	Daemon := os.Getenv("TAM_DAEMON")
	switch Daemon {
	case "Client":
		FileName = "tam-local.db"
	case "Server":
		FileName = "tam-remote.db"
	}
	val := path.Join(base_dir, FileName)
	return val
}

func GetConfigPath() string {
	base_dir := GetDataDir()
	val := path.Join(base_dir, "settings.json")
	return val
}
