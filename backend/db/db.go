package db

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var Pool *pgxpool.Pool

func ConnectDB(connStr string) {
	var err error
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		log.Fatalf("Unable to parse DATABASE_URL: %v", err)
	}

	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 15 * time.Minute

	// Connect with retry logic
	for i := 0; i < 5; i++ {
		Pool, err = pgxpool.NewWithConfig(context.Background(), config)
		if err == nil {
			err = Pool.Ping(context.Background())
			if err == nil {
				log.Println("Successfully connected to PostgreSQL via pgxpool")
				return
			}
		}
		log.Printf("Failed to connect to DB, retrying in 2 seconds... (%d/5)", i+1)
		time.Sleep(2 * time.Second)
	}

	log.Fatalf("Could not connect to database after retries: %v", err)
}
