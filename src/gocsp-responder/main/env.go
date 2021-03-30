package main

import (
	"os"
	"strconv"
)

func getStringEnv(key string, fallback string) string {
	s, exists := os.LookupEnv(key)
	if !exists {
		return fallback
	}
	return s
}

func getIntEnv(key string, fallback int) int {
	s, exists := os.LookupEnv(key)
	if !exists {
        	return fallback
	}
	i, _ := strconv.ParseInt(s, 10, 32)
	return int(i)
}

func getBoolEnv(key string, fallback bool) bool {
	s, exists := os.LookupEnv(key)
	if !exists {
        	return fallback
	}
	b, _ := strconv.ParseBool(s)
	return b
}
