package db

import (
	"context"
	"log"
	"time"

	"secretary/timeutil"

	"github.com/jackc/pgx/v5"
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

	// Every connection runs in Brasília time. The database container was
	// initialized with TimeZone = UTC, so without this the TIMESTAMP columns
	// (which hold a Brasília wall clock) are compared against NOW() as if they
	// were UTC, and reminders fire three hours off.
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET TIME ZONE '"+timeutil.ZoneName+"'")
		return err
	}

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
