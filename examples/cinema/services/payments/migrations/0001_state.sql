-- +goose Up
CREATE TABLE state (key TEXT PRIMARY KEY, document TEXT NOT NULL);
