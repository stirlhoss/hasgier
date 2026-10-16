package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Port                 int
	DatabaseURL          string
	WooURL               string
	WooConsumerKey       string
	WooConsumerSecret    string
	WooWebhookSecret     string
	ShipStationAPIKey    string
	ShipStationAPISecret string
	QBClientID           string
	QBClientSecret       string
	QBRedirectURI        string
	QBEnvironment        string // "sandbox" | "production"
}

func Load() (*Config, error) {
	port, err := strconv.Atoi(getEnv("PORT", "8080"))
	if err != nil {
		return nil, fmt.Errorf("invalid PORT: %w", err)
	}

	cfg := &Config{
		Port:                 port,
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		WooURL:               os.Getenv("WOO_URL"),
		WooConsumerKey:       os.Getenv("WOO_CONSUMER_KEY"),
		WooConsumerSecret:    os.Getenv("WOO_CONSUMER_SECRET"),
		WooWebhookSecret:     os.Getenv("WOO_WEBHOOK_SECRET"),
		ShipStationAPIKey:    os.Getenv("SHIPSTATION_API_KEY"),
		ShipStationAPISecret: os.Getenv("SHIPSTATION_API_SECRET"),
		QBClientID:           os.Getenv("QB_CLIENT_ID"),
		QBClientSecret:       os.Getenv("QB_CLIENT_SECRET"),
		QBRedirectURI:        os.Getenv("QB_REDIRECT_URI"),
		QBEnvironment:        getEnv("QB_ENVIRONMENT", "sandbox"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	required := map[string]string{
		"DATABASE_URL":           c.DatabaseURL,
		"WOO_URL":                c.WooURL,
		"WOO_CONSUMER_KEY":       c.WooConsumerKey,
		"WOO_CONSUMER_SECRET":    c.WooConsumerSecret,
		"WOO_WEBHOOK_SECRET":     c.WooWebhookSecret,
		"SHIPSTATION_API_KEY":    c.ShipStationAPIKey,
		"SHIPSTATION_API_SECRET": c.ShipStationAPISecret,
		"QB_CLIENT_ID":           c.QBClientID,
		"QB_CLIENT_SECRET":       c.QBClientSecret,
		"QB_REDIRECT_URI":        c.QBRedirectURI,
	}

	for name, value := range required {
		if value == "" {
			return fmt.Errorf("missing required environment variable: %s", name)
		}
	}

	return nil
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
