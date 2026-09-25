# secondmem integration

How Aros uses [secondmem](https://github.com/Rinil-Parmar/secondmem) as a shared
memory layer, and what secondmem does internally. Line references are to the
state of both repos on 2026-09-25.

Design rationale and the "why optional" decision: [ADR-0008](adr/0008-secondmem-optional-best-effort-memory.md).

---

## 1. The integration surface

Aros does **not** link secondmem as a library. It shells out to the `secondmem`
binary and exchanges plain strings. The entire contract is two commands:

```
secondmem ask    "<question>"        → answer on stdout
secondmem ingest "<text>" | <path>   → stores content
```

Implementation: `memory/secondmem.go`.

### Read path — `Ask`

| Call site | Phase | Retrieval key passed to secondmem |
|---|---|---|
| `planner/planner.go:37` | plan (CLI) | the full task description |
| `tui/phases.go:92` | plan (TUI) | the full task description |
| `worker/worker.go:306` | work, once per task | **`task.Title` only** |
| `tui/model.go:1088` | chat | the user's raw question |

The returned string is injected verbatim into the agent prompt under a
`MEMORY CONTEXT:` / `RELEVANT CONTEXT FROM SHARED MEMORY:` heading
(`worker.BuildWorkPrompt`, `tui/prompts.go`). There is no re-ranking, no
relevance threshold and no length cap on what gets injected.

`Ask` returns `""` on any error, so a memory failure degrades prompt quality and
can never fail a phase. It uses `cmd.Output()`, so **only stdout is read**;
stderr is discarded.

### Write path — `Ingest`

| Call site | Trigger | Content stored |
|---|---|---|
| `cmd/plan_cmd.go:78` | plan approved (CLI) | `"Approved plan for <project>:\n<plan>"` |
| `tui/phases.go:65` (`ingestAsync`) | plan approved (TUI) | same |
| `divider/divider.go:76` | divide approved (CLI) | `"Task manifest for <project>:\n<summary>"` |
| `worker/worker.go:360` | each task completes | `"Task <id> (<title>) result:\n<output>"` |

> **Asymmetry, by omission not design:** the CLI ingests the task manifest after
> divide; the **TUI does not**. `tui/phases.go` `runDivide`'s `onYes` saves the
> manifest and state but never calls `Ingest`. A TUI-only workflow therefore has
> no manifest in memory.

Task-result ingestion runs on a detached goroutine *after* the task is already
marked `done` (`worker/worker.go:358-361`), so it never delays scheduling.

### Operational limits

| Constant | Value | Where |
|---|---|---|
| `askTimeout` | 30s | `memory/secondmem.go:18` |
| `ingestTimeout` | 90s | `memory/secondmem.go:19` |
| `inlineLimit` | 500 chars | `memory/secondmem.go:21` |

Content over `inlineLimit` is written to a `0600` temp file and passed as a
**path** (`secondmem ingest <path>`), because secondmem's `ingest` accepts either
text or a file path. This avoids the argv size limit. The temp file is removed
after the call.

If `secondmem.enabled = true` but the binary is not on `PATH`, `memory.New`
flips it to disabled rather than failing every call (`memory/secondmem.go:34-38`).

### Two precise defects in this seam

Both stem from Aros treating **all stdout as an answer**:

1. **The empty-KB message is injected as context.** When nothing matches,
   `agent/ask.go:101` returns the literal string
   `"No relevant knowledge found in your knowledge base. Try ingesting some content first."`
   with a nil error and exit code 0. Aros sees non-empty stdout and pastes that
   sentence into the agent's prompt as memory context.
2. **A graph-open warning is injected as context.** `cmd/ask.go:50` prints
   `"Warning: could not open graph database: …"` to **stdout**, not stderr. If
   the SQLite file is locked or missing, that line is prepended to the answer and
   ends up in the prompt.

Fix on either side: have secondmem emit warnings on stderr and a sentinel/exit
code for "no results", or have Aros filter known non-answers. Neither is done.

---

## 2. secondmem internals

### Core idea

Markdown files on disk are the **source of truth**; SQLite is a **rebuildable
index**. You can read, grep, git-commit and hand-edit the notes; the database is
a cache over them. `rebalance` reconciles the index back to the filesystem.

```
~/.secondmem/
├── config.toml
├── secondmem.db              SQLite index ("LORE-GRAPH")
└── knowledge/
    ├── hierarchy.md          root table of contents
    └── <topic>/
        ├── hierarchy.md      per-directory ToC
        └── <name>.md         one note
```

### Schema — `graph/migrations/`

```sql
nodes    (id, file_path UNIQUE, directory, title, summary, keywords,
          tags, node_type, line_count, created_at, updated_at)
edges    (source_id, target_id, edge_type, weight, UNIQUE(source,target))
nodes_fts FTS5(title, summary, keywords, content=nodes, content_rowid=id)
chunks   (node_id, chunk_index, content, embedding BLOB,
          UNIQUE(node_id, chunk_index))
```

`nodes_fts` is an FTS5 **external-content** table — it holds the index only, no
copy of the rows, and is kept in sync by three triggers (`nodes_ai`, `nodes_ad`,
`nodes_au`) on insert/delete/update. Migrations are embedded in the binary with
`go:embed` and tracked in `schema_migrations`.

### Ingest pipeline — `agent/ingest.go`

| Step | Action | Notes |
|---|---|---|
| 1 | Exact dedup | SHA256 of the content; skip unless `--force` |
| 2 | Classify | LLM → `{directory, filename, summary, keywords, related_topics}` |
| 3 | Semantic dedup | top-3 FTS candidates → LLM judges overlap → skip if duplicate |
| 4 | Write note | `knowledge/<directory>/<filename>.md` |
| 5 | Update hierarchy | directory `hierarchy.md` + root `hierarchy.md` |
| 6 | Upsert graph node | then for each related topic: FTS-search it, add edge `weight 0.7` |
| 7 | Chunk + embed | see below |
| 8 | Cross-references | bidirectional links between related notes |

### Embeddings — exact parameters

```go
// agent/ingest.go:188
chunks := chunkText(content, 400, 50)   // 400-word window, 50-word overlap
```

- **Model:** `nomic-embed-text`, **hardcoded** at `providers/ollama.go:100`
  (768 dimensions; the storage code itself is dimension-agnostic).
- **Transport:** `POST {ollama.url}/api/embeddings`, default
  `http://localhost:11434`.
- **Storage:** little-endian `float32` BLOB, 4 bytes per dimension
  (`float32ToBlob`, `graph/chunks.go:102`) → ≈3 KB per chunk at 768 dims.
- **Re-index:** `DeleteChunksByNode` then re-insert, so a re-ingest replaces all
  chunks for that note.

Chunking is a plain sliding **word-count** window (`strings.Fields`), not
markdown-aware — it will split a fenced code block or a table mid-structure.

### Retrieval — three tiers, in order (`agent/ask.go`)

1. **Vector search** — embed the question, then `SearchByVector(qVec, 6)`:
   loads **every** chunk embedding in the DB, scores each by cosine similarity in
   Go, sorts, takes the top 6, then dedupes to one entry per file.
2. **FTS5 fallback** (only if tier 1 produced nothing) — an LLM call first
   rewrites the question into 5–8 keywords, then FTS5 with prefix matching
   (`transformer*`) OR-joined, `Search(query, 5)`, followed by **graph
   expansion**: each hit's `edges` neighbours are pulled in too, capped at 7 files.
3. **Hierarchy walk** (last resort) — feed root `hierarchy.md` to the LLM, ask
   which ≤3 directories are relevant, read every `.md` in them.

The selected files are concatenated into one prompt with an
"answer ONLY from this context, cite the file paths" system prompt, plus the
user's `skill.md`, and sent to the chat model.

### Models

| Role | Default | Alternatives |
|---|---|---|
| Reasoning (classify, dedup, answer, keyword extraction) | `llama3.2` via Ollama `/api/chat` | `openai` → `gpt-4o`; `copilot` → `gpt-4o-mini` |
| Embeddings | `nomic-embed-text` via Ollama (768-d) | `openai` / `copilot` → `text-embedding-3-small` (1536-d) |

Selected by `model.provider` and `embed.provider` in
`~/.secondmem/config.toml`. Ollama is the default so no API key is needed.

### Rebalance — `cmd/rebalance.go`

Seven steps, `--dry-run` supported:

1. **Split** notes over `knowledge_base.max_file_lines` (default **1116**) —
   LLM finds topic boundaries.
2. **Orphans** — files on disk missing from their `hierarchy.md` → regenerate it.
3. **Hierarchy links** — remove dead links to deleted files.
4. **Cross-reference integrity** — verify both directions still resolve.
5. **Merge candidates** — report note pairs with **>70%** keyword overlap.
6. **Graph sync** — reconcile `nodes` rows against the filesystem.
7. **Rebuild root ToC**.

This is the loop that keeps individual notes from growing past what fits in an
LLM context window.

---

## 3. Known limits of secondmem (as used by Aros)

| Limit | Detail | Impact on Aros |
|---|---|---|
| **O(N) vector scan** | Every query loads all embeddings into memory and scores them in Go; no ANN index (no HNSW/IVF, no `sqlite-vec`) | Fine at ~10³ chunks; at 10⁵ the 30s `askTimeout` starts truncating phases |
| **Norms recomputed per query** | `cosine()` (`graph/chunks.go:86`) recomputes both magnitudes on every comparison | Pre-normalising at write time would reduce this to a dot product |
| **Silent dimension mismatch** | `cosine()` returns `0` when `len(a) != len(b)`. Switching `embed.provider` ollama→openai makes every stored 768-d chunk score 0 against a 1536-d query | Retrieval silently degrades to the FTS tier with **no error anywhere** |
| **Embedding model hardcoded** | `nomic-embed-text` is a literal; config exposes `embed.provider` but no `embed.model` | Cannot tune embedding quality without a code change |
| **Retrieval key is thin** | Aros's work phase asks using `task.Title` only (~6 words), not the description | Weakest link in the whole memory path; the description is available and unused |
| **No re-embed on external edit** | Editing a note outside `ingest` leaves chunks stale; `rebalance` syncs the graph, not vectors | Memory can silently disagree with the notes |
| **Format-blind chunking** | 400-word windows split code blocks and tables | Retrieved context can be syntactically broken |
| **stdout is the API** | Warnings and the empty-KB message travel on stdout (see §1) | Both get injected into agent prompts |

---

## 4. Turning it off

```toml
# ~/.aros/config.toml  or  ./.aros/config.toml
[secondmem]
enabled = false
```

Aros then behaves identically minus the memory context. All automated tests run
this way (`memory.New("", false)`), so nothing in CI depends on a local model.
