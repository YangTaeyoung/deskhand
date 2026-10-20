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
