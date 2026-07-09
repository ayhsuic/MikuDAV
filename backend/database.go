package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

const createTablesSQL = `
CREATE TABLE IF NOT EXISTS artists (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS albums (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        TEXT NOT NULL,
    artist_id   BIGINT NOT NULL,
    year        SMALLINT,
    cover_path  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (artist_id) REFERENCES artists(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS songs (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title         TEXT NOT NULL,
    artist_id     BIGINT NOT NULL,
    album_id      BIGINT,
    track_number  SMALLINT,
    disc_number   SMALLINT DEFAULT 1,
    genre         TEXT,
    lyrics        TEXT,
    duration      REAL,
    bitrate       INTEGER,
    sample_rate   INTEGER,
    file_path     TEXT NOT NULL UNIQUE,
    file_size     BIGINT,
    cover_path    TEXT,
    last_modified TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (artist_id) REFERENCES artists(id) ON DELETE CASCADE,
    FOREIGN KEY (album_id) REFERENCES albums(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_songs_title ON songs(title);
CREATE INDEX IF NOT EXISTS idx_songs_artist ON songs(artist_id);
CREATE INDEX IF NOT EXISTS idx_songs_album ON songs(album_id);
CREATE INDEX IF NOT EXISTS idx_songs_album_order ON songs(album_id, disc_number, track_number);
`

func initDB(pool *pgxpool.Pool) error {
	_, err := pool.Exec(context.Background(), createTablesSQL)
	if err != nil {
		return fmt.Errorf("数据库初始化失败: %w", err)
	}
	log.Println("数据库表初始化完成")
	return nil
}
