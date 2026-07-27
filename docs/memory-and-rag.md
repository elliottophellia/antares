# Memory and retrieval

Two different things that are easy to confuse.

**Memory** is what the agent chooses to remember about you and your work. Small,
durable, and injected into every system prompt.

**Retrieval** is search over a body of documents you indexed. Large, on demand,
and only what a query pulls back.

## Memory

The agent decides what is worth keeping and writes it with the `memory` tool.
Good memories are the things that stay true: which database this project uses,
that you prefer no semicolons, that the staging box is `srv-2`.

```yaml
memory:
  memory_enabled: true
  user_profile_enabled: true
  memory_char_limit: 4000
  search_limit: 10
  nudge_interval: 20
```

`memory_char_limit` bounds what goes into the prompt. Past it, the most recently
used survive.

### Managing it

```
/memory                    recent memories
/memory postgres           search
/remember staging is srv-2
/remember db: postgres 16 on srv-2
/forget db
```

`key: value` sets an explicit key, which is what `/forget` takes. The dashboard's
Memory page lists everything with a delete on each.

### What not to store

Anything that changes — today's date, the current branch, what you are working
on this afternoon. A stale memory is worse than no memory, because the agent
believes it.

## Retrieval

Point it at documentation, a codebase, or a pile of notes, and the agent can
search meaning rather than exact words.

```yaml
rag:
  enabled: true
  provider: builtin
  embed_model: text-embedding-3-small
  chunk_size: 1200
  chunk_overlap: 150
  top_k: 8
  hybrid: true
```

### Indexing

```bash
antares rag index ~/projects/myapp
antares rag index ~/notes --collection notes
```

Or from the dashboard's Memory & RAG page, or with the `rag_index` tool during a
conversation.

Collections keep bodies separate so a search can be scoped.

### Two backends

**`builtin`** embeds with your configured model and stores vectors in the
Antares database. Nothing else to run. With `hybrid: true` it fuses dense
similarity with lexical matching, which is noticeably better for code and exact
identifiers — reciprocal-rank fusion over both result sets.

**`enowx`** delegates to an [enowx-rag](https://github.com/enowdev/enowx-rag)
daemon, which adds reranking and near-duplicate compression.

```yaml
rag:
  provider: enowx
  enowx_base_url: http://127.0.0.1:8080
  enowx_project: antares
  enowx_rerank: true
  enowx_compress: true
```

The tools are the same either way. Switching backends does not change how the
agent works, only what comes back.

### Chunking

`chunk_size` is characters, not tokens. 1200 with 150 overlap suits prose and
code alike. Larger chunks give more context per hit and fewer hits; smaller
chunks are more precise and more numerous.

Re-index after changing it — existing chunks keep the old size.

## Session search

Separate from both, and needs no setup: `session_search` is full-text search
across every past conversation, backed by FTS5 on SQLite and `tsvector` on
Postgres.

"What did we decide about the schema last week" is a session search, not a
retrieval query.

## Which to use

| You want | Use |
|---|---|
| A fact about you or the project, always available | Memory |
| Something said in a past conversation | Session search |
| Something in a document or codebase you indexed | Retrieval |
| Something on the web | `web_search` or `browser` |
