-- +goose Up
CREATE TABLE screenings (
 id TEXT PRIMARY KEY, cinema_id TEXT NOT NULL, auditorium_id TEXT NOT NULL, movie_id TEXT NOT NULL,
 starts_at_ms INTEGER NOT NULL, ends_at_ms INTEGER NOT NULL, base_price_minor INTEGER NOT NULL,
 currency TEXT NOT NULL, hold_ms INTEGER NOT NULL, cancel_cutoff_ms INTEGER NOT NULL,
 canceled INTEGER NOT NULL CHECK(canceled IN (0,1)), seats TEXT NOT NULL CHECK(json_valid(seats))
);
CREATE INDEX screenings_auditorium ON screenings(cinema_id,auditorium_id,starts_at_ms);
INSERT INTO screenings
SELECT j.key,json_extract(j.value,'$.cinemaId'),json_extract(j.value,'$.auditoriumId'),json_extract(j.value,'$.movieId'),
json_extract(j.value,'$.startsAtMs'),json_extract(j.value,'$.endsAtMs'),json_extract(j.value,'$.basePriceMinor'),
json_extract(j.value,'$.currency'),json_extract(j.value,'$.holdMs'),json_extract(j.value,'$.cancelCutoffMs'),
json_extract(j.value,'$.canceled'),json_extract(j.value,'$.seats')
FROM state s,json_each(s.document,'$.screenings') j WHERE s.key='schedule';
DROP TABLE state;
