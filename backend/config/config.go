package config

import (
	"os"
)

type Config struct {
	DatabaseURL  string
	OpenAIAPIKey string
	Port         string
}

func LoadConfig() *Config {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://secretary_user:secretary_password@localhost:5432/secretary_db?sslmode=disable"
	}

	openAIKey := os.Getenv("OPENAI_API_KEY")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	return &Config{
		DatabaseURL:  dbURL,
		OpenAIAPIKey: openAIKey,
		Port:         port,
	}
}
