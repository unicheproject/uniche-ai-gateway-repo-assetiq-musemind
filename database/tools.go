package database

import (
	"context"
	"fmt"
	"time"
)

const tsLayout = "2006-01-02T15:04:05.000000000Z07:00"

type Tool struct {
	ID           int64
	Name         string
	Description  string
	CallbackURL  string
	RegisteredAt time.Time
}

func (db *DB) InsertTool(ctx context.Context, name, description, callbackURL string) (Tool, error) {
	now := time.Now().UTC().Round(0)
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO tools (name, description, callback_url, registered_at) VALUES (?, ?, ?, ?) RETURNING id`,
		name, description, callbackURL, now.Format(tsLayout),
	).Scan(&id)
	if err != nil {
		return Tool{}, fmt.Errorf("insert tool: %w", err)
	}
	return Tool{ID: id, Name: name, Description: description, CallbackURL: callbackURL, RegisteredAt: now}, nil
}

func (db *DB) ListTools(ctx context.Context) ([]Tool, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, description, callback_url, registered_at FROM tools ORDER BY registered_at`)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	defer rows.Close()

	var tools []Tool
	for rows.Next() {
		var t Tool
		var ts string
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.CallbackURL, &ts); err != nil {
			return nil, fmt.Errorf("scan tool: %w", err)
		}
		t.RegisteredAt, _ = time.Parse(tsLayout, ts)
		tools = append(tools, t)
	}
	return tools, rows.Err()
}
