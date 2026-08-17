package store

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"
)

const memoryCols = `id,scope,scope_key,mem_key,content,tags,source,pinned,created_at,updated_at`

func scanMemory(sc interface{ Scan(...any) error }) (*Memory, error) {
	var m Memory
	var created, upd int64
	if err := sc.Scan(&m.ID, &m.Scope, &m.ScopeKey, &m.Key, &m.Content, &m.Tags, &m.Source, &m.Pinned, &created, &upd); err != nil {
		return nil, err
	}
	m.CreatedAt, m.UpdatedAt = fromMS(created), fromMS(upd)
	return &m, nil
}

func (s *sqlStore) PutMemory(ctx context.Context, m *Memory) error {
	now := time.Now()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	if m.Tags == "" {
		m.Tags = "[]"
	}
	if m.Scope == "" {
		m.Scope = "global"
	}
	_, err := s.exec(ctx, `INSERT INTO memories (`+memoryCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`+
		onConflict("id", "content=EXCLUDED.content, mem_key=EXCLUDED.mem_key, tags=EXCLUDED.tags, pinned=EXCLUDED.pinned, updated_at=EXCLUDED.updated_at"),
		m.ID, m.Scope, m.ScopeKey, m.Key, m.Content, m.Tags, m.Source, m.Pinned, ms(m.CreatedAt), ms(m.UpdatedAt))
	return err
}

func (s *sqlStore) ListMemories(ctx context.Context, scope, scopeKey string, limit int) ([]Memory, error) {
	var where []string
	var args []any
	if scope != "" && scope != "all" {
		where = append(where, "scope=?")
		args = append(args, scope)
	}
	if scopeKey != "" {
		where = append(where, "scope_key=?")
		args = append(args, scopeKey)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.query(ctx, `SELECT `+memoryCols+` FROM memories`+clause+
		` ORDER BY pinned DESC, updated_at DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Memory, 0, limit)
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *sqlStore) SearchMemories(ctx context.Context, query string, limit int) ([]Memory, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return s.ListMemories(ctx, "", "", limit)
	}
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	var (
		rows *sql.Rows
		err  error
	)
	if s.dialect == "sqlite" {
		rows, err = s.query(ctx, `SELECT `+prefixCols("m", memoryCols)+` FROM memories_fts f
			JOIN memories m ON m.id = f.memory_id WHERE memories_fts MATCH ?
			ORDER BY bm25(memories_fts) LIMIT ?`, ftsQuery(query), limit)
	} else {
		rows, err = s.query(ctx, `SELECT `+memoryCols+` FROM memories
			WHERE to_tsvector('simple', content) @@ plainto_tsquery('simple', ?)
			ORDER BY ts_rank(to_tsvector('simple', content), plainto_tsquery('simple', ?)) DESC LIMIT ?`,
			query, query, limit)
	}
	if err != nil {
		rows, err = s.query(ctx, `SELECT `+memoryCols+` FROM memories WHERE LOWER(content) LIKE ?
			ORDER BY updated_at DESC LIMIT ?`, "%"+strings.ToLower(query)+"%", limit)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()
	out := make([]Memory, 0, limit)
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func prefixCols(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ",")
}

func (s *sqlStore) DeleteMemory(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `DELETE FROM memories WHERE id=?`, id)
	return err
}

func (s *sqlStore) ClearMemories(ctx context.Context, scope, scopeKey string) (int64, error) {
	q := `DELETE FROM memories WHERE pinned = FALSE`
	var args []any
	if scope != "" && scope != "all" {
		q += ` AND scope=?`
		args = append(args, scope)
	}
	if scopeKey != "" {
		q += ` AND scope_key=?`
		args = append(args, scopeKey)
	}
	res, err := s.exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ---- RAG chunks -------------------------------------------------------------

func (s *sqlStore) PutChunks(ctx context.Context, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, s.rebind(
		`INSERT INTO rag_chunks (id,collection,doc_id,path,chunk_index,content,embedding,meta,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`+onConflict("id", "content=EXCLUDED.content, embedding=EXCLUDED.embedding, meta=EXCLUDED.meta")))
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range chunks {
		if c.CreatedAt.IsZero() {
			c.CreatedAt = time.Now()
		}
		meta, _ := c.Meta.Value()
		if _, err := stmt.ExecContext(ctx, c.ID, c.Collection, c.DocID, c.Path, c.Index, c.Content,
			encodeEmbedding(c.Embedding), meta, ms(c.CreatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SearchChunks ranks by cosine similarity, optionally blended with a lexical
// pass (reciprocal-rank fusion) when hybrid is set.
func (s *sqlStore) SearchChunks(ctx context.Context, collection string, embedding []float32, text string, topK int, hybrid bool) ([]Chunk, []float64, error) {
	if topK <= 0 {
		topK = 8
	}
	rows, err := s.query(ctx,
		`SELECT id,collection,doc_id,path,chunk_index,content,embedding,meta,created_at FROM rag_chunks WHERE collection=?`, collection)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	type scored struct {
		c     Chunk
		dense float64
		lex   float64
	}
	var all []scored
	lowerText := strings.ToLower(text)
	terms := strings.Fields(lowerText)

	for rows.Next() {
		var (
			c   Chunk
			emb []byte
			ts  int64
		)
		if err := rows.Scan(&c.ID, &c.Collection, &c.DocID, &c.Path, &c.Index, &c.Content, &emb, &c.Meta, &ts); err != nil {
			return nil, nil, err
		}
		c.CreatedAt = fromMS(ts)
		c.Embedding = decodeEmbedding(emb)
		sc := scored{c: c, dense: cosine(embedding, c.Embedding)}
		if hybrid && len(terms) > 0 {
			lower := strings.ToLower(c.Content)
			hits := 0
			for _, t := range terms {
				if strings.Contains(lower, t) {
					hits++
				}
			}
			sc.lex = float64(hits) / float64(len(terms))
		}
		all = append(all, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	if hybrid {
		dense := make([]int, len(all))
		lex := make([]int, len(all))
		for i := range all {
			dense[i], lex[i] = i, i
		}
		sort.Slice(dense, func(a, b int) bool { return all[dense[a]].dense > all[dense[b]].dense })
		sort.Slice(lex, func(a, b int) bool { return all[lex[a]].lex > all[lex[b]].lex })
		rrf := make(map[int]float64, len(all))
		for rank, idx := range dense {
			rrf[idx] += 1.0 / float64(60+rank)
		}
		for rank, idx := range lex {
			rrf[idx] += 1.0 / float64(60+rank)
		}
		order := make([]int, len(all))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool { return rrf[order[a]] > rrf[order[b]] })
		outC := make([]Chunk, 0, topK)
		outS := make([]float64, 0, topK)
		for _, idx := range order {
			if len(outC) >= topK {
				break
			}
			outC = append(outC, all[idx].c)
			outS = append(outS, rrf[idx])
		}
		return outC, outS, nil
	}

	sort.Slice(all, func(a, b int) bool { return all[a].dense > all[b].dense })
	if len(all) > topK {
		all = all[:topK]
	}
	outC := make([]Chunk, len(all))
	outS := make([]float64, len(all))
	for i, sc := range all {
		outC[i], outS[i] = sc.c, sc.dense
	}
	return outC, outS, nil
}

func (s *sqlStore) DeleteCollection(ctx context.Context, collection string) (int64, error) {
	res, err := s.exec(ctx, `DELETE FROM rag_chunks WHERE collection=?`, collection)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *sqlStore) ListCollections(ctx context.Context) ([]string, error) {
	rows, err := s.query(ctx, `SELECT DISTINCT collection FROM rag_chunks ORDER BY collection`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
