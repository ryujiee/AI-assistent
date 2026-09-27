package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"secretary/config"
	"secretary/db"
)

// runMigrateCommand implements "secretary migrate status|apply [version]".
// It is the only way versioned migrations reach a database.
func runMigrateCommand(args []string) int {
	usage := "usage: secretary migrate status | apply [up-to-version]"
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}

	cfg := config.LoadConfig()
	db.ConnectDB(cfg.DatabaseURL)
	defer db.Pool.Close()
	ctx := context.Background()

	switch args[0] {
	case "status":
		states, err := db.MigrationStatus(ctx, db.Pool)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		for _, s := range states {
			state := "pending"
			switch {
			case s.Modified:
				state = "MODIFIED after apply"
			case s.Applied:
				state = "applied " + s.AppliedAt.Format("2006-01-02 15:04")
			}
			fmt.Printf("%s  %-28s %s\n", s.Version, s.Name, state)
		}
		return 0

	case "apply":
		upTo := ""
		if len(args) > 1 {
			upTo = args[1]
		}
		applied, err := db.ApplyMigrations(ctx, db.Pool, upTo)
		if len(applied) > 0 {
			fmt.Println("applied:", strings.Join(applied, ", "))
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		if len(applied) == 0 {
			fmt.Println("nothing to apply")
		}
		return 0
	}

	fmt.Fprintln(os.Stderr, usage)
	return 2
}
