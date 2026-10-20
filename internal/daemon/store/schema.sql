CREATE TABLE IF NOT EXISTS hosts (
	host_id    TEXT PRIMARY KEY,
	name       TEXT NOT NULL UNIQUE,
	address    TEXT NOT NULL DEFAULT '',
	port       INTEGER NOT NULL DEFAULT 0,
	agent_fp   TEXT NOT NULL DEFAULT '',
	os         TEXT NOT NULL DEFAULT '',
	arch       TEXT NOT NULL DEFAULT '',
	ssh_alias  TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	last_seen  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS jobs (
	job_id      TEXT PRIMARY KEY,
	host_id     TEXT NOT NULL,
	kind        TEXT NOT NULL,
	request_id  TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL,
	params_json TEXT NOT NULL DEFAULT '{}',
	result_json TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL,
	summary     TEXT NOT NULL DEFAULT '',
	finished_at TEXT NOT NULL DEFAULT '',
	remote_job  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS jobs_host ON jobs (host_id, created_at);
CREATE INDEX IF NOT EXISTS jobs_remote ON jobs (host_id, remote_job);

CREATE TABLE IF NOT EXISTS events (
	seq          INTEGER PRIMARY KEY AUTOINCREMENT,
	ts           TEXT NOT NULL,
	host_id      TEXT NOT NULL DEFAULT '',
	job_id       TEXT NOT NULL DEFAULT '',
	kind         TEXT NOT NULL,
	payload_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS requests (
	request_id  TEXT PRIMARY KEY,
	host_id     TEXT NOT NULL,
	op          TEXT NOT NULL,
	state       TEXT NOT NULL,
	result_json TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS timeline (
	seq            INTEGER PRIMARY KEY AUTOINCREMENT,
	ts             TEXT NOT NULL,
	host_id        TEXT NOT NULL DEFAULT '',
	actor          TEXT NOT NULL,
	client_id      TEXT NOT NULL DEFAULT '',
	client_name    TEXT NOT NULL DEFAULT '',
	kind           TEXT NOT NULL,
	summary        TEXT NOT NULL,
	outcome        TEXT NOT NULL,
	job_id         TEXT NOT NULL DEFAULT '',
	artifacts_json TEXT NOT NULL DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS timeline_host ON timeline (host_id, seq);

CREATE TABLE IF NOT EXISTS transfers (
	transfer_id TEXT PRIMARY KEY,
	host_id     TEXT NOT NULL,
	direction   TEXT NOT NULL,
	local_path  TEXT NOT NULL,
	remote_path TEXT NOT NULL,
	on_conflict TEXT NOT NULL,
	sep         TEXT NOT NULL DEFAULT '/',
	state       TEXT NOT NULL,
	planned     INTEGER NOT NULL DEFAULT 0,
	folder      INTEGER NOT NULL DEFAULT 0,
	outcome     TEXT NOT NULL DEFAULT '',
	error_json  TEXT NOT NULL DEFAULT '',
	rev         INTEGER NOT NULL DEFAULT 0,
	actor       TEXT NOT NULL DEFAULT '',
	client_id   TEXT NOT NULL DEFAULT '',
	client_name TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL,
	finished_at TEXT NOT NULL DEFAULT '',
	job_id      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS transfers_host ON transfers (host_id, created_at);

CREATE TABLE IF NOT EXISTS transfer_files (
	transfer_id TEXT NOT NULL,
	seq         INTEGER NOT NULL,
	rel         TEXT NOT NULL,
	dir         INTEGER NOT NULL DEFAULT 0,
	size        INTEGER NOT NULL DEFAULT 0,
	mtime       TEXT NOT NULL DEFAULT '',
	sha256      TEXT NOT NULL DEFAULT '',
	state       TEXT NOT NULL,
	bytes_done  INTEGER NOT NULL DEFAULT 0,
	outcome     TEXT NOT NULL DEFAULT '',
	path        TEXT NOT NULL DEFAULT '',
	error_json  TEXT NOT NULL DEFAULT '',
	commit_rid  TEXT NOT NULL DEFAULT '',
	part        INTEGER NOT NULL DEFAULT 0,
	refresh     INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (transfer_id, seq)
);
