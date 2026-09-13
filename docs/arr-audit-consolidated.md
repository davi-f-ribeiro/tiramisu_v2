# Auditoria ARR/Bazarr — Tiramisu v2

**Data:** 2026-09-13
**Repositório:** `/opt/data/tiramisu_v2`
**HEAD:** `5717ff3` — "feat(arr): raw_title persistence + full quality parser prioritizing raw titles"
**Baseline:** `tiramisu_v1.1_hardened.zip` (idêntico ao HEAD — todo o bloco ARR já está nos 5 commits mais recentes)

## Estado do Repositório
- `git status` → limpo (sem mudanças não comitadas)
- Go **NÃO instalado** no host — testes automatizados não podem ser executados localmente

---

## FASE 1 — INVENTÁRIO DA ALTERAÇÃO

### Arquivos novos (em relação ao zip baseline)
| Arquivo | Justificativa |
|---------|--------------|
| `docs/fork-snapshot-pre-merge.md` | Documentação — sem impacto funcional |
| `docs/prowlarr-adapter.md` | Documentação — sem impacto funcional |
| `docs/screenshots/*.png`, `.gif` | Assets — sem impacto funcional |
| `docs/stub-lifecycle.html` | Documentação — sem impacto funcional |

### Arquivos modificados (em relação ao zip baseline)
| Arquivo | O que mudou | Por que |
|---------|------------|---------|
| `main.go` | Bloco ARR Virtual Catalog (~linha 4346) | Integração ARR/Bazarr |
| `internal/arr/server.go` | +492 linhas (handler completo, SignalR, parseQuality) | Facade Radarr/Sonarr |
| `internal/arr/backfill.go` | Novo — scan de stubs em disco | Backfill de stubs pré-existentes |
| `internal/arr/backfill_test.go` | Novo — testes de backfill | Testes unitários |
| `internal/arr/models.go` | Novas structs (RadarrMovie, SonarrSeries, etc.) | Modelos da API |
| `internal/arr/server_test.go` | Novo — testes de handler | Testes HTTP |
| `internal/metadb/db.go` | Tabela `arr_media` adicionada ao schema | SQLite para catálogo |
| `internal/metadb/arr.go` | +151 linhas (Upsert/Delete/Get ARR) | CRUD no SQLite |
| `internal/metadb/arr_test.go` | Novo — testes de ARR | Testes DB |
| `internal/metadb/episodes.go` | `UpsertEpisode` adicionado | Estado de episódios TV |
| `internal/syncer/engines/movie_go.go` | Campo `metadb *metadb.DB`, chamada `UpsertARRMovie` | Registro ARR automático |
| `internal/syncer/engines/movies.go` | (ver diff) | — |
| `internal/syncer/engines/tv_go.go` | Campos `db`/`metadb`, chamadas `UpsertARRSeries`/`UpsertARREpisode` | Registro ARR automático TV |
| `internal/vfs/metadata.go` | (ver diff) | — |
| `go.mod` | Dependência `modernc.org/sqlite` adicionada | SQLite backend |

### Funções novas introduzidas
| Função | Pacote | Responsabilidade |
|--------|--------|-----------------|
| `parseQualityFromFilename(rawTitle, fileName)` | `arr` | Inferência de qualidade a partir do nome do arquivo |
| `handleManualSync(w, r)` | `arr` | Endpoint POST /sync — executa backfill sob demanda |
| `handleSignalRNegotiate(w, r)` | `arr` | Resposta dummy para handshake SignalR do Bazarr |
| `handleEmptyArray(w, r)` | `arr` | Resposta `[]` para endpoints que Bazarr consulta mas não usa |
| `StartStandaloneListeners(ctx, store)` | `arr` | Inicia servidores HTTP separados em :7878 e :8989 |
| `RunBackfill(ctx, moviesDir, tvDir, db, logger)` | `arr` | Scan de stubs em disco → populate `arr_media` |
| `backfillMovies(ctx, dir, db, logger, stats)` | `arr` | Backfill de filmes |
| `backfillSeries(ctx, dir, db, logger, stats)` | `arr` | Backfill de séries + episódios |
| `parseStub(path)` | `arr` | Parse de stub .mkv JSON |
| `extractTMDBID(filename)` | `arr` | Extração de TMDB ID do filename |
| `extractYear(filename)` | `arr` | Extração de ano do filename |
| `GetMovies`, `GetMovieByID`, `GetSeries`, `GetSeriesByID`... | `arr` | MediaStore read methods |
| `UpsertARRMovie`, `UpsertARRSeries`, `UpsertARREpisode`, `DeleteARRMediaByPath` | `metadb` | CRUD da tabela arr_media |
| `GetARRMovieByPath`, `GetARREpisodeByPath` | `metadb` | Leitura por path |
| `UpsertEpisode` | `metadb` | Estado de episódio TV (não ARR) |

---

## FASE 2 — DETECÇÃO DE DUPLICAÇÃO

### ❌ CRÍTICO: Fallback `tmdbId/tvdbId = arr_media.id`

**Arquivo:** `internal/arr/server.go`, linhas 532-535 e 629-631

```go
// handleMovieList:
if m.TmdbID <= 0 {
    m.TmdbID = m.ID  // ← Fallback para ID interno
}

// handleSeriesList:
if s.TvdbID <= 0 {
    s.TvdbID = s.ID  // ← Fallback para ID interno
}
```

**Problema:** Quando `tmdb_id = 0` ou `tvdb_id = 0` na tabela `arr_media`, o handler substitui pelo `arr_media.id` (autoincrement interno). Se dois filmes diferentes tiverem `tmdb_id = 0`, ambos serão retornados com o **mesmo tmdbId**, fazendo o Bazarr tratar como duplicata.

**Por que existe:** Commit `24d90ff` — "fallback tmdbId/tvdbId ao ID interno para evitar UNIQUE constraint no Bazarr".

### ✅ NÃO há duplicação de parser de stub
- `parseStub()` em `backfill.go` é exclusiva do backfill
- `buildExistingMovieIndex()` em `movie_go.go` faz parsing inline
- **Veredicto:** acceptable — backfill e engine têm objetivos diferentes

### ✅ ReadMetadataFromFile não duplicada
- A função `ReadMetadataFromFile` do VFS original permanece intacta
- Nenhum novo parser de MkvJSON foi criado

### ✅ Nenhuma duplicação de queries SQL ARR
- `arr/server.go` e `metadb/arr.go` têm implementações duplicadas das queries Upsert — intencional

---

## FASE 3 — CONCORRÊNCIA E RACE CONDITIONS

### Goroutines introduzidas
| Goroutine | O que faz | Risco |
|-----------|-----------|-------|
| `arr.StartStandaloneListeners` — goroutine 1 | Inicia servidor Radarr em `:7878` | Baixo |
| `arr.StartStandaloneListeners` — goroutine 2 | Inicia servidor Sonarr em `:8989` | Baixo |
| `arr.StartStandaloneListeners` — goroutine 3 | Espera `<-ctx.Done()` e faz shutdown | Baixo — 5s timeout |
| `main.go` ~4364 | Canela `arrCancel()` quando `backgroundStopChan` dispara | Médio |
| `main.go` ~4373 | Backfill async: `go func() { RunBackfill(...) }()` | **ALTO** |

### ⚠️ Risco de backfill async vs. handlers HTTP
- `arrStore` é `arr.NewDBStore(stateDB.SQL())` — compartilha o mesmo `*sql.DB` dos handlers HTTP
- `SetMaxOpenConns(1)` → queries HTTP podem **bloquear** se o backfill estiver segurando uma query de longa duração

### ✅ Mutex em `Handler` — uso correto
- `appName` é o único campo mutável protegido por mutex
- Não há `SetAppName` chamado após a criação do handler
- **Veredicto:** mutex presente mas provavelmente desnecessário

---

## FASE 4 — SQLITE / METADB

### Schema `arr_media`
```sql
CREATE TABLE IF NOT EXISTS arr_media (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    media_type       TEXT NOT NULL,
    tmdb_id          INTEGER DEFAULT 0,
    tvdb_id          INTEGER DEFAULT 0,
    imdb_id          TEXT DEFAULT '',
    raw_title        TEXT DEFAULT '',
    series_id        INTEGER DEFAULT 0,
    season_number    INTEGER DEFAULT 0,
    episode_number   INTEGER DEFAULT 0,
    title            TEXT NOT NULL,
    year             INTEGER DEFAULT 0,
    path             TEXT NOT NULL UNIQUE,   -- ← UNIQUE constraint
    size             INTEGER DEFAULT 0,
    updated_at       TEXT DEFAULT (datetime('now'))
);
```

### ✅ Constraints adequadas
- `path UNIQUE` → previne duplicatas por caminho
- `ON CONFLICT(path) DO UPDATE SET` → idempotente

### ⚠️ Sem índice composto para queries comuns
- `idx_arr_media_type ON arr_media(media_type)` — bom para filtrar por tipo
- `idx_arr_media_series ON arr_media(series_id)` — bom para buscar episódios de uma série
- **Falta:** índice em `(media_type, tmdb_id)` para queries como "encontrar filme por TMDB"

### ✅ IDs externos: `tmdb_id`, `tvdb_id`, `imdb_id` são campos separados

---

## FASE 5 — FLUXO DE CRIAÇÃO

### MovieSync → ARR
```
MovieGoEngine.processMovie() [movie_go.go:563]
  → e.createMKV(mkvPath, ...) [movie_go.go:563]
  → if e.metadb != nil { e.metadb.UpsertARRMovie(...) } [movie_go.go:569-574]
```
- ✅ `metadb` é `*metadb.DB` injetado via `MovieEngineConfig.MetaDB`
- ✅ `MetaDB` é o **mesmo DB** (`stateDB`) usado pelo resto do sistema

### MovieSync removal → ARR
```
MovieGoEngine.removeStub() [movie_go.go:256-259]
  → if e.metadb != nil { _ = e.metadb.DeleteARRMediaByPath(ctx, path) }
```
- ✅ Chamada em `removeStub` — coberto para remoção

### TVSync → ARR
```
TVGoEngine.processShow() [tv_go.go:688-697]
  → if e.db != nil { id, err := e.db.UpsertARRSeries(...) }
  → e.arrSeriesID = id  ← ID salvo no campo do engine

TVGoEngine.processFullpack() [tv_go.go:1196-1200]
  → if e.db != nil && e.arrSeriesID > 0 { e.db.UpsertARREpisode(...) }

TVGoEngine.processSingle() [tv_go.go:1287-1291]
  → if e.db != nil && e.arrSeriesID > 0 { e.db.UpsertARREpisode(...) }
```
- ✅ `e.db` é `*metadb.DB` (campo `db`)

### ⚠️ TVGoEngine: campo `metadb` não usado
```go
// tv_go.go:47-48
db           *metadb.DB // V1.7.1: Optional SQLite backend
metadb       *metadb.DB // ARR catalog backend
```
- `metadb` é um campo separado — **nunca é preenchido em NewTVGoEngine**
- Todas as chamadas ARR usam `e.db`, não `e.metadb`
- **Veredicto:** `e.metadb` é código morto — nunca é preenchido nem lido

### ⚠️ TVGoEngine.removeStub NÃO remove do catálogo ARR
- `MovieGoEngine.removeStub` chama `DeleteARRMediaByPath` (linha 258) — ✅ coberto
- `TVGoEngine.removeStub` NÃO chama `DeleteARRMediaByPath` — apenas FUSE cleanup + GoStorm
- **Episódios TV removidos via `removeStub` NÃO são removidos do catálogo ARR**

---

## FASE 6 — METADATA

### ✅ `ReadMetadataFromFile` continua sendo a única fonte de parsing
- Não há novo parser substituindo `ReadMetadataFromFile`

### ✅ Qualidade
- `parseQualityFromFilename()` é exclusiva da facade ARR
- Não toca no core de metadata do Tiramisu

### Campos ARR gravados no stub
- `UpsertARRMovie`: `tmdbID`, `imdbID`, `title`, `rawTitle`, `year`, `path`, `size`
- `UpsertARRSeries`: `tmdbID`, `tvdbID`, `imdbID`, `title`, `seriesDir`
- `UpsertARREpisode`: `seriesID`, `season`, `episode`, `title`, `path`, `size`
- ✅ Todos os campos essenciais estão presentes

---

## FASE 7 — BACKFILL

### Complexidade algorítmica
- `backfillMovies`: O(N) — uma passada nos arquivos .mkv do moviesDir
- `backfillSeries`: O(N + M) onde N = episódios, M = showDirs

### ⚠️ N×M search linear no backfill de episódios
```go
// backfill.go:252-261
for _, ep := range episodes {
    var found *seriesEntry
    for i := range seriesList {  // ← O(M) por episódio
        if seriesList[i].showDir == ep.showDir {
            found = &seriesList[i]
            break
        }
    }
    ...
}
```
- **Recomendação:** Substituir por map `showDir → seriesID`

### ⚠️ Backfill async sem proteção de shutdown
```go
// main.go ~4373
go func() {
    stats, err := arr.RunBackfill(context.Background(), moviesDir, tvDir, arrStore, logger)
```
- Usa `context.Background()` — não é canelado pelo `backgroundStopChan`
- Se o Tiramisu receber SIGTERM durante o backfill, a goroutine continua rodando
- O DB pode ser fechado pelo shutdown, enquanto o backfill tenta escrever
- **Risco:** `write to closed DB` ou panic

---

## FASE 8 — API ARR

### Endpoints implementados
| Endpoint | Status | Observação |
|----------|--------|------------|
| `GET /api/v3/system/status` | ✅ | Retorna app name dinâmico (Radarr/Sonarr) |
| `GET /api/v3/rootfolder` | ✅ | Retorna `[]` |
| `GET /api/v3/qualityprofile` | ✅ | Retorna `[]` |
| `GET /api/v3/tag` | ✅ | Retorna `[]` |
| `GET /api/v3/languageprofile` | ✅ | Retorna `[]` |
| `GET /api/v3/movie` | ✅ | Lista filmes com movieFile subobject |
| `GET /api/v3/movie/{id}` | ✅ | Detail por ID |
| `GET /api/v3/series` | ✅ | Lista séries |
| `GET /api/v3/series/{id}` | ✅ | Detail por ID |
| `GET /api/v3/episode?seriesId=` | ✅ | Lista episódios de série |
| `GET /api/v3/episodefile?seriesId=` | ✅ | Lista episode files |
| `POST /api/v3/arr/sync` | ✅ | Manual sync (backfill) |
| SignalR negotiate | ✅ | Dummy response |
| `GET /api/v3/command` | ✅ | Retorna `[]` |
| `GET /api/v3/health` | ✅ | Retorna `[]` |
| `GET /api/v3/diskspace` | ✅ | Retorna `[]` |
| Legacy unversioned routes | ✅ | `/api/system/status`, etc. |

### ⚠️ `handleMovieList` e `handleMovieDetail` — duplicação de código
- As linhas 526-562 e 586-615 são **quase idênticas**
- Mesma lógica: set HasFile/Monitored, fallback tmdbId, calcular cleanTitle, criar MovieFile
- **Não é duplicação crítica** — handlers separados com responsabilidades distintas

### ✅ IDs retornados corretamente
- `RadarrMovie.ID` → `arr_media.id` ✓
- `RadarrMovie.TmdbID` → `arr_media.tmdb_id` ✓ (mas com fallback para ID interno)
- `SonarrSeries.TvdbID` → `arr_media.tvdb_id` ✓ (mas com fallback para ID interno)
- `SonarrEpisode.EpisodeFileID` → `arr_media.id` ✓

---

## FASE 9 — SIGNALR

### ✅ Dummy SignalR — validado pelo Bazarr
- Resposta é determinística (sempre a mesma estrutura)
- Sem estado compartilhado
- Sem goroutines — é apenas um handler HTTP
- Não interfere nos endpoints HTTP (rotas separadas)

---

## FASE 10 — MODELOS E CAMPOS ARTIFICIAIS

### Valores inventados verificados
| Campo | Valor | Inventado? |
|-------|-------|------------|
| `Version: "4.8.2"` | Versão fake do Servarr | Aceitável |
| `IsLinux: true`, `IsDocker: true` | Fixed | Aceitável |
| `QualityModel` default | ID:7, WEBDL-1080p | Potencial problema se arquivo real não é 1080p |
| `QualityModel` via `parseQualityFromFilename` | Inferido do nome | ✅ Melhor que fixo |
| `dateAdded` | `time.Now()` | Sem risco funcional |
| `episodeFileId` | `e.ID` (arr_media.id) | ✅ Não é fabricado |
| `seriesId` | `seriesID` (de arr_media) | ✅ Correto |

### ⚠️ `defaultQuality` não é mais usado
- `var defaultQuality = QualityModel{...}` (linha 334-346) é declarada mas **nunca referenciada**
- **Veredicto:** código morto — pode ser removido

---

## FASE 11 — PERFORMANCE

### Criação de stub → ARR
```
createMKV → UpsertARRMovie/UpsertARRSeries/UpsertARREpisode
```
- ✅ **Uma única query SQL** por criação — não há scan de filesystem ou reconstrução completa

### ⚠️ Regex recompilada em loops (backfill.go:275)
```go
func extractTMDBID(filename string) int {
    re := regexp.MustCompile(`[Tt]mdb[ _]?(\d+)`)  // ← recompilada sempre
```
- **Fix:** mover para variável package-level

---

## FASE 12 — API / HTTP CONCURRENCY

### Handlers podem receber requisições simultâneas?
- ✅ `http.ServeMux` é safe para concurrent requests
- ✅ `MediaStore` é `*DBStore` que lê de `*sql.DB` com `SetMaxOpenConns(1)`
- ✅ `Handler.mu` protege apenas `appName`
- ✅ Estado imutável após init

---

## FASE 13 — SHUTDOWN

### ⚠️ Backfill async não é interrompido
- `context.Background()` não é canelado
- Se SIGTERM/SIGINT chega durante backfill, goroutine continua
- Se o DB é fechado durante o backfill: **panic ou error silencioso**

### ✅ Servidores HTTP têm graceful shutdown
- 5s timeout — razoável

---

## FASE 17 — AUDITORIA DE FUNÇÕES NOVAS

| Função | Ação | Motivo |
|--------|------|--------|
| `parseQualityFromFilename` | **KEEP** | Resolve qualidade do arquivo — Bazarr exige |
| `handleManualSync` | **KEEP** | Endpoint para sync sob demanda |
| `handleSignalRNegotiate` | **KEEP** | Bazarr exige handshake SignalR |
| `handleEmptyArray` | **KEEP** | Endpoints que Bazarr consulta |
| `StartStandaloneListeners` | **KEEP** | Servidores separados Radarr/Sonarr |
| `RunBackfill` | **KEEP** | Migration/reconciliation |
| `backfillMovies` | **KEEP** | Parte do backfill |
| `backfillSeries` | **KEEP** | Parte do backfill |
| `parseStub` | **KEEP** | Parse minimalista de stub |
| `extractTMDBID` | **REFACTOR** | Regex recompilada em loop |
| `extractYear` | **KEEP** | Simples e raramente usada |
| `GetMovies`, `GetMovieByID`... | **KEEP** | MediaStore interface necessária |
| `UpsertARRMovie`... | **KEEP** | CRUD necessário |
| `defaultQuality` | **REMOVE** | Código morto |
| `e.metadb` (TVGoEngine field) | **REMOVE** | Campo não usado |

---

## FASE 19 — COMPATIBILIDADE COM UPSTREAM

### Arquivos modificados que podem gerar conflitos
| Arquivo | Frequência upstream | Risco |
|---------|---------------------|-------|
| `main.go` | Alta | **Médio** — bloco ARR isolado em seção comentada |
| `go.mod` | Média | **Baixo** — apenas adição de sqlite |
| `internal/syncer/engines/movie_go.go` | Baixa-Média | **Baixo** — adição condicional |
| `internal/syncer/engines/tv_go.go` | Baixa-Média | **Baixo** — adição condicional |
| `internal/metadb/db.go` | Baixa | **Baixo** — nova tabela não interfere |

### ✅ Alterações são minimamente invasivas
- Campos `metadb` nos engines são opcionais (`nil` por default)
- Chamadas ARR são condicionais (`if e.metadb != nil`)
- Novo bloco em `main.go` é autocontido (4346-4382)

---

# RELATÓRIO FINAL

## BLOCKERS 🔴

### B1: Fallback `tmdbId/tvdbId = arr_media.id` (server.go:532-535, 589-591, 629-631, 655-657)
- **Descrição:** Quando `tmdb_id` ou `tvdb_id` é 0/NULL, o handler usa `arr_media.id` como fallback. Isso faz dois filmes diferentes sem TMDB parecerem o mesmo filme para o Bazarr.
- **Impacto:** Duplicatas no Bazarr, UNIQUE constraint errors, ou perda de mídia.
- **Recomendação:** Retirar o fallback `m.TmdbID = m.ID` e `s.TvdbID = s.ID`. Testar com Bazarr para confirmar se rejeita TMDB=0.

### B2: TVGoEngine campo `metadb` não usado (tv_go.go:48)
- **Descrição:** `metadb *metadb.DB` é declarado mas nunca preenchido nem lido.
- **Impacto:** Código morto que pode confundir desenvolvedores futuros.
- **Recomendação:** Remover o campo.

### B3: Backfill async não respeita shutdown (main.go:~4373)
- **Descrição:** Backfill usa `context.Background()` — não é interrompido por SIGTERM.
- **Impacto:** Goroutine pode tentar escrever em DB fechado durante shutdown.
- **Recomendação:** Passar um contexto canelado pelo `backgroundStopChan`.

### B4: TVGoEngine.removeStub NÃO remove do catálogo ARR (tv_go.go ~213-255)
- **Descrição:** `MovieGoEngine.removeStub` chama `DeleteARRMediaByPath` (movie_go.go:258), mas `TVGoEngine.removeStub` NÃO chama — apenas FUSE cleanup + GoStorm.
- **Impacto:** Episódios TV removidos ficam registrados no catálogo ARR mesmo após exclusão física.
- **Recomendação:** Adicionar `DeleteARRMediaByPath` em `TVGoEngine.removeStub`.

## HIGH 🟠

### H1: Regex recompilada em loop (backfill.go:275)
- `regexp.MustCompile` chamado a cada chamada de `extractTMDBID`.
- **Recomendação:** Mover para variável package-level.

### H2: Backfill N×M linear (backfill.go:252-261)
- Busca linear de seriesList para cada episódio.
- **Recomendação:** Usar map `showDir→seriesID`.

### H3: defaultQuality código morto (server.go:334-346)
- Variável declarada mas nunca usada.
- **Recomendação:** Remover.

### H4: `SetAppName`/`getAppName` com mutex provavelmente desnecessário (server.go:267-277)
- `appName` é setado no constructor e nunca modificado.
- **Recomendação:** Remover mutex e métodos de getter/setter.

## MEDIUM 🟡

### M1: Sem índice composto para queries comuns (metadb/db.go:168-169)
- Apenas índices em `media_type` e `series_id` separados.
- **Recomendação:** Adicionar índice em `(media_type, tmdb_id)` se queries por TMDB forem frequentes.

### M2: `handleMovieList` e `handleMovieDetail` duplicam lógica
- ~30 linhas idênticas entre os handlers.
- **Recomendação:** Refatorar para função interna comum.

### M3: Sem teste de race condition (Go não instalado)
- **Recomendação:** Executar `go test -race ./...` em ambiente com Go instalado.

## LOW 🔵

### L1: `DateAdded: time.Now()` em cada GET
- Sem impacto funcional, mas muda a cada request.

### L2: Hardcoded versions (`4.8.2`, `2024-01-15`)
- Sem impacto funcional para integração Bazarr.

---

## TEST RESULTS

```
go version  → GO NÃO INSTALADO NO HOST
go test ./... → NÃO EXECUTADO
go test -race ./... → NÃO EXECUTADO
go vet ./... → NÃO EXECUTADO
gofmt -l . → NÃO EXECUTADO
staticcheck ./... → NÃO EXECUTADO
```

---

## RECOMENDAÇÃO FINAL

### BLOQUEADO — resolver itens B1, B2, B3, B4 antes de commit

**B1** é o mais crítico: o fallback `tmdbId = arr_media.id` é semanticamente incorreto e causa duplicatas no Bazarr. A correção mínima é:
- Retirar o fallback `m.TmdbID = m.ID` e `s.TvdbID = s.ID`
- Testar com Bazarr para confirmar se rejeita TMDB=0

**B2**, **B3** e **B4** são correções limpas sem risco de regressão.

Após correção dos BLOCKERS:
1. `gofmt -l .` (confirmar clean)
2. `go test ./...` (confirmar pass)
3. `go test -race ./...` (confirmar clean)
4. `go vet ./...` (confirmar clean)
5. Mostrar diff final para revisão
6. Commit
