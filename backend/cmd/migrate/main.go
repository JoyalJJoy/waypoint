package main

import (
	"fmt"
	"log"
	"os"

	"github.com/joyaljoy/waypoint/internal/config"
	"github.com/joyaljoy/waypoint/internal/platform/migrations"
)

func main() {
	url, err := config.LoadMigrate()
	if err != nil {
		log.Fatal(err)
	}

	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "up":
		err = migrations.Up(url)
	case "down":
		err = migrations.Down(url)
	case "status":
		err = migrations.Status(url)
	default:
		err = fmt.Errorf("unknown command %q (use up, down, or status)", command)
	}
	if err != nil {
		log.Fatal(err)
	}
}
