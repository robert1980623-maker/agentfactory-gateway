package main

import (
	"bufio"
	"log"
	"os"
	"strings"
)

func init() {
	loadEnv()
}

func loadEnv() {
	log.Printf("[DEBUG] Working dir: %s", os.Getenv("PWD"))
	f, err := os.Open(".env")
	if err != nil {
		log.Printf("[DEBUG] No .env file found at %s, skipping load: %v", os.Getenv("PWD"), err)
		return
	}
	defer f.Close()
	
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if _, exists := os.LookupEnv(key); !exists {
				os.Setenv(key, val)
			}
		}
	}
	log.Println("Loaded .env file")
}
