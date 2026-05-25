package config

import "os"

type Config struct {
	SlackBotToken string
	SlackAppToken string
	PythonBin     string
	AFCLIBin      string
	AFWorkDir     string
}

func Load() Config {
	return Config{
		SlackBotToken: getEnv("SLACK_BOT_TOKEN", ""),
		SlackAppToken: getEnv("SLACK_APP_TOKEN", ""),
		PythonBin:     getEnv("PYTHON_BIN", "python3"),
		AFCLIBin:      getEnv("AF_CLI_BIN", ""),
		AFWorkDir:     getEnv("AF_WORK_DIR", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
