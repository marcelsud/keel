-- +goose Up
CREATE TABLE state (key TEXT PRIMARY KEY, document TEXT NOT NULL);
CREATE TABLE movies (id TEXT PRIMARY KEY, title TEXT NOT NULL, duration_ms INTEGER NOT NULL CHECK(duration_ms > 0));
CREATE TABLE cinemas (id TEXT PRIMARY KEY, name TEXT NOT NULL, auditoriums TEXT NOT NULL CHECK(json_valid(auditoriums)));
CREATE TABLE fixtures (id TEXT PRIMARY KEY);
