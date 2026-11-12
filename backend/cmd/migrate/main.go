package main

import (
	"fmt"
	"log"
	"os"

	"github.com/joyaljoy/waypoint/internal/config"
	"github.com/joyaljoy/waypoint/internal/platform/migrations"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "up":
		err = migrations.Up(cfg.MigrateDatabaseURL)
	case "down":
		err = migrations.Down(cfg.MigrateDatabaseURL)
	case "status":
		err = migrations.Status(cfg.MigrateDatabaseURL)
	default:
		err = fmt.Errorf("unknown command %q (use up, down, or status)", command)
	}
	if err != nil {
		log.Fatal(err)
	}
}
