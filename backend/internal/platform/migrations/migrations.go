package migrations

import (
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed sql/*.sql
var files embed.FS

func Up(databaseURL string) error {
	return run(databaseURL, goose.Up)
}

func Down(databaseURL string) error {
	return run(databaseURL, goose.Down)
}

func Status(databaseURL string) error {
	return run(databaseURL, goose.Status)
}

func run(databaseURL string, fn func(*sql.DB, string, ...goose.OptionsFunc) error) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(files)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	if err := fn(db, "sql"); err != nil {
		return fmt.Errorf("migration: %w", err)
	}
	return nil
}
