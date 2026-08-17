package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// ---- cron -------------------------------------------------------------------

const cronCols = `id,name,schedule,prompt,enabled,target,timezone,last_run,next_run,last_state,created_at,updated_at,meta`

func scanCronJob(sc interface{ Scan(...any) error }) (*CronJob, error) {
	var (
		j                CronJob
		last, next       sql.NullInt64
		created, updated int64
	)
	if err := sc.Scan(&j.ID, &j.Name, &j.Schedule, &j.Prompt, &j.Enabled, &j.Target, &j.Timezone,
		&last, &next, &j.LastState, &created, &updated, &j.Meta); err != nil {
		return nil, err
	}
	j.LastRun, j.NextRun = fromMSPtr(last), fromMSPtr(next)
	j.CreatedAt, j.UpdatedAt = fromMS(created), fromMS(updated)
	return &j, nil
}

func (s *sqlStore) PutCronJob(ctx context.Context, j *CronJob) error {
	now := time.Now()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = now
	}
	j.UpdatedAt = now
	meta, _ := j.Meta.Value()
	_, err := s.exec(ctx, `INSERT INTO cron_jobs (`+cronCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`+
		onConflict("id", `name=EXCLUDED.name, schedule=EXCLUDED.schedule, prompt=EXCLUDED.prompt,
			enabled=EXCLUDED.enabled, target=EXCLUDED.target, timezone=EXCLUDED.timezone,
			last_run=EXCLUDED.last_run, next_run=EXCLUDED.next_run, last_state=EXCLUDED.last_state,
			updated_at=EXCLUDED.updated_at, meta=EXCLUDED.meta`),
		j.ID, j.Name, j.Schedule, j.Prompt, j.Enabled, j.Target, j.Timezone,
		msPtr(j.LastRun), msPtr(j.NextRun), j.LastState, ms(j.CreatedAt), ms(j.UpdatedAt), meta)
	return err
}

func (s *sqlStore) GetCronJob(ctx context.Context, id string) (*CronJob, error) {
	j, err := scanCronJob(s.row(ctx, `SELECT `+cronCols+` FROM cron_jobs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return j, err
}

func (s *sqlStore) ListCronJobs(ctx context.Context) ([]CronJob, error) {
	rows, err := s.query(ctx, `SELECT `+cronCols+` FROM cron_jobs ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CronJob{}
	for rows.Next() {
		j, err := scanCronJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *sqlStore) DeleteCronJob(ctx context.Context, id string) error {
	if _, err := s.exec(ctx, `DELETE FROM cron_runs WHERE job_id=?`, id); err != nil {
		return err
	}
	_, err := s.exec(ctx, `DELETE FROM cron_jobs WHERE id=?`, id)
	return err
}

func (s *sqlStore) PutCronRun(ctx context.Context, r *CronRun) error {
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now()
	}
	_, err := s.exec(ctx, `INSERT INTO cron_runs (id,job_id,status,output,error,session_id,started_at,finished_at)
		VALUES (?,?,?,?,?,?,?,?)`+
		onConflict("id", "status=EXCLUDED.status, output=EXCLUDED.output, error=EXCLUDED.error, finished_at=EXCLUDED.finished_at"),
		r.ID, r.JobID, r.Status, r.Output, r.Error, r.SessionID, ms(r.StartedAt), msPtr(r.FinishedAt))
	return err
}

func (s *sqlStore) ListCronRuns(ctx context.Context, jobID string, limit int) ([]CronRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT id,job_id,status,output,error,session_id,started_at,finished_at FROM cron_runs`
	args := []any{}
	if jobID != "" {
		q += ` WHERE job_id=?`
		args = append(args, jobID)
	}
	q += ` ORDER BY started_at DESC LIMIT ?`
	rows, err := s.query(ctx, q, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CronRun, 0, limit)
	for rows.Next() {
		var (
			r        CronRun
			started  int64
			finished sql.NullInt64
		)
		if err := rows.Scan(&r.ID, &r.JobID, &r.Status, &r.Output, &r.Error, &r.SessionID, &started, &finished); err != nil {
			return nil, err
		}
		r.StartedAt, r.FinishedAt = fromMS(started), fromMSPtr(finished)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- usage ------------------------------------------------------------------

func (s *sqlStore) RecordUsage(ctx context.Context, u *Usage) error {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	_, err := s.exec(ctx, `INSERT INTO usage_records (id,session_id,provider,model,tokens_in,tokens_out,cache_read,cost,source,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		u.ID, u.SessionID, u.Provider, u.Model, u.TokensIn, u.TokensOut, u.CacheRead, u.Cost, u.Source, ms(u.CreatedAt))
	return err
}

// UsageSeries buckets usage by hour or day. Bucketing happens in Go so the query
// stays dialect-neutral.
func (s *sqlStore) UsageSeries(ctx context.Context, since time.Time, bucket string) ([]UsagePoint, error) {
	rows, err := s.query(ctx, `SELECT created_at, tokens_in, tokens_out, cost FROM usage_records WHERE created_at >= ? ORDER BY created_at`, ms(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	layout := "2006-01-02"
	hourly := strings.EqualFold(bucket, "hour")
	if hourly {
		layout = "2006-01-02T15:00"
	}

	// Pre-seed every bucket across the whole range so a chart always spans the
	// full window as a continuous line — empty periods read as zero rather than
	// collapsing the series to a single point.
	index := map[string]*UsagePoint{}
	order := []string{}
	step := 24 * time.Hour
	start := since.Truncate(24 * time.Hour)
	if hourly {
		step = time.Hour
		start = since.Truncate(time.Hour)
	}
	for tcur := start; !tcur.After(time.Now()); tcur = tcur.Add(step) {
		key := tcur.Format(layout)
		if _, ok := index[key]; !ok {
			p := &UsagePoint{Bucket: key}
			index[key] = p
			order = append(order, key)
		}
	}

	for rows.Next() {
		var (
			ts      int64
			in, out int64
			cost    float64
		)
		if err := rows.Scan(&ts, &in, &out, &cost); err != nil {
			return nil, err
		}
		key := fromMS(ts).Format(layout)
		p, ok := index[key]
		if !ok {
			// A record outside the pre-seeded grid (clock skew, edge of range):
			// keep it rather than drop it.
			p = &UsagePoint{Bucket: key}
			index[key] = p
			order = append(order, key)
		}
		p.TokensIn += in
		p.TokensOut += out
		p.Cost += cost
		p.Calls++
	}

	sort.Strings(order)
	out := make([]UsagePoint, 0, len(order))
	for _, k := range order {
		out = append(out, *index[k])
	}
	return out, rows.Err()
}

func (s *sqlStore) UsageByModel(ctx context.Context, since time.Time) ([]ModelUsage, error) {
	rows, err := s.query(ctx, `SELECT model, provider, COUNT(*), COALESCE(SUM(tokens_in),0), COALESCE(SUM(tokens_out),0), COALESCE(SUM(cost),0)
		FROM usage_records WHERE created_at >= ? GROUP BY model, provider ORDER BY SUM(tokens_in)+SUM(tokens_out) DESC`, ms(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModelUsage{}
	for rows.Next() {
		var m ModelUsage
		if err := rows.Scan(&m.Model, &m.Provider, &m.Calls, &m.TokensIn, &m.TokensOut, &m.Cost); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---- pairings ---------------------------------------------------------------

func (s *sqlStore) PutPairing(ctx context.Context, p *Pairing) error {
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	_, err := s.exec(ctx, `INSERT INTO pairings (id,platform,external_id,display_name,status,code,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)`+
		onConflict("platform, external_id", "display_name=EXCLUDED.display_name, status=EXCLUDED.status, code=EXCLUDED.code, updated_at=EXCLUDED.updated_at"),
		p.ID, p.Platform, p.ExternalID, p.DisplayName, p.Status, p.Code, ms(p.CreatedAt), ms(p.UpdatedAt))
	return err
}

func (s *sqlStore) GetPairing(ctx context.Context, platform, externalID string) (*Pairing, error) {
	var (
		p            Pairing
		created, upd int64
	)
	err := s.row(ctx, `SELECT id,platform,external_id,display_name,status,code,created_at,updated_at
		FROM pairings WHERE platform=? AND external_id=?`, platform, externalID).
		Scan(&p.ID, &p.Platform, &p.ExternalID, &p.DisplayName, &p.Status, &p.Code, &created, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.CreatedAt, p.UpdatedAt = fromMS(created), fromMS(upd)
	return &p, nil
}

func (s *sqlStore) ListPairings(ctx context.Context) ([]Pairing, error) {
	rows, err := s.query(ctx, `SELECT id,platform,external_id,display_name,status,code,created_at,updated_at FROM pairings ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pairing{}
	for rows.Next() {
		var (
			p            Pairing
			created, upd int64
		)
		if err := rows.Scan(&p.ID, &p.Platform, &p.ExternalID, &p.DisplayName, &p.Status, &p.Code, &created, &upd); err != nil {
			return nil, err
		}
		p.CreatedAt, p.UpdatedAt = fromMS(created), fromMS(upd)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *sqlStore) DeletePairing(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `DELETE FROM pairings WHERE id=?`, id)
	return err
}

// ---- key/value --------------------------------------------------------------

func (s *sqlStore) GetKV(ctx context.Context, key string) (string, error) {
	var v string
	err := s.row(ctx, `SELECT kv_value FROM kv WHERE kv_key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *sqlStore) SetKV(ctx context.Context, key, value string) error {
	_, err := s.exec(ctx, `INSERT INTO kv (kv_key,kv_value,updated_at) VALUES (?,?,?)`+
		onConflict("kv_key", "kv_value=EXCLUDED.kv_value, updated_at=EXCLUDED.updated_at"),
		key, value, ms(time.Now()))
	return err
}

func (s *sqlStore) DeleteKV(ctx context.Context, key string) error {
	_, err := s.exec(ctx, `DELETE FROM kv WHERE kv_key=?`, key)
	return err
}

func (s *sqlStore) ListKV(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.query(ctx, `SELECT kv_key, kv_value FROM kv WHERE kv_key LIKE ?`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
