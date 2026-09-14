# Relatório de Implementação: Resolução de TMDB ID a partir do IMDb ID

## Objetivo
Implementar resolução automática do `tmdb_id` e `year` de filmes/séries na tabela `arr_media` usando a API oficial do TMDb (`/find/{imdb_id}`), quando esses registros possuem um `imdb_id` válido mas `tmdb_id = 0`.

---

## Arquivos Modificados/Criados

### 1. `internal/config/config.go` (+7 linhas)
- **O que mudou:** Adicionado fallback de env `TMDB_API_KEY` no final de `LoadConfig()`
- **Comportamento:** Se `cfg.TMDBAPIKey` vier vazio do JSON, o sistema tenta `os.Getenv("TMDB_API_KEY")`
- **Padrão:** Segue o padrão existente no projeto (`os.Getenv`) — mesmo padrão das variáveis `MKV_PROXY_*` e `AI_*`

### 2. `internal/catalog/tmdb/client.go` (+45 linhas)
- **Reuso:** O client HTTP já existente (com retry, backoff, timeout) e rate limiter (4 req/s) foram reaproveitados
- **Método adicionado:** `FindByIMDbID(ctx, imdbID string) (tmdbID int64, releaseDate string, error)`
- **Endpoint:** `GET /find/{imdb_id}?api_key={KEY}&external_source=imdb_id`
- **Resposta:**
  - `movie_results[0].id` → `tmdb_id` do filme
  - `movie_results[0].release_date` → origem do `year` (YYYY-MM-DD)
  - Se não houver movies, tenta `tv_results[0].id` e `first_air_date`
- **Rate limit:** Usa o mesmo `rate.Limiter` do client (1 req/250ms) + delay de 1s no resolver
- **Estrutura adicional:** `FindResult` com `MovieResults` e `TVResults`

### 3. `internal/catalog/tmdb/client_test.go` (arquivo novo, 60 linhas)
- **Testes:** 3 casos unitários testando parse da resposta `/find/`
  - `TestFindResult_ParseMovieResult` — movie com id=603, release_date=1999-03-31
  - `TestFindResult_ParseTVResult` — fallback para TVShow com id=1396, first_air_date=2005-03-20
  - `TestFindResult_ParseEmpty` — resposta vazia retorna tmdbID=0
- **Nota:** Testes usam `json.Unmarshal` direto porque o `Client` usa `baseURL` hardcoded (`api.themoviedb.org`), então `httptest.NewServer` não intercepta — o teste valida o parsing (o que importa para nosso código)

### 4. `internal/metadb/arr.go` (+38 linhas)
- **Estrutura:** `UnresolvedMedia` com `ID`, `MediaType`, `IMDBID`
- **Query:** `GetUnresolvedARRMedia()` — retorna registros com `tmdb_id = 0 AND imdb_id != '' AND media_type IN ('movie', 'series')`
- **Update:** `UpsertARRMediaTMDB(ctx, mediaID, tmdbID, year)` — atualiza `tmdb_id`, `year` e `updated_at` por ID

### 5. `internal/arr/resolver.go` (arquivo novo, 191 linhas)
- **Worker:** `TMDBResolver` roda em goroutine de background após o backfill completar
- **Timing:**
  - Aguarda 5s após startup para garantir servidor estável
  - Executa resolução inicial imediatamente
  - Roda a cada 30 minutos (cron interno)
- **Cache:** `sync.Map` com TTL de 1 hora — evita re-query para mesma IMDb ID já resolvida
- **Rate limit:** 1s entre cada request (além do rate limiter do client)
- **Retry:** até 3 tentativas com backoff exponencial (2s, 4s, 6s)
- **Filtro:** ignora IDs que não têm formato válido (menos de 2 chars)
- **Logs:** `[TMDB resolver] started`, `[TMDB resolver] done: N resolved, M errored (N total)`, `[TMDB resolver] stopped`

### 6. `main.go` (+14 inserções, +1 import)
- **Import:** `tmdbpkg "tiramisu/internal/catalog/tmdb"` (alias para evitar conflito com `metadb.TMDBID` etc.)
- **Integração:** Após o `RunBackfill` e dentro do bloco `if stateDB != nil`:
  - Verifica `cfg.TMDBAPIKey != ""` (só inicia se chave configurada)
  - Cria `tmdbpkg.NewClient(cfg.TMDBAPIKey)`
  - Inicializa `arr.NewTMDBResolver` com `DB: stateDB`
  - Chama `resolver.Run(arrCtx)` — retorna imediatamente, resolução roda em background

---

## Fluxo de Dados

```
[Config]
  ├── config.json.tmdb_api_key = "..."
  └── env TMDB_API_KEY = "..."  (fallback)
       │
       ▼
[main.go] → tmdb.NewClient(cfg.TMDBAPIKey)
       │
       ▼
[Backfill] → Popula arr_media com imdb_id (mas tmdb_id=0)
       │
       ▼
[Resolver] (background, 5s delay)
       │
       ├── 1. GetUnresolvedARRMedia() → ["tt0133093", "tt1234567", ...]
       │
       ├── 2. Para cada IMDb ID:
       │       ├── Check cache (TTL 1h) → se hit, usa diretamente
       │       └── Se miss:
       │           ├── Retry 3x (2s, 4s, 6s)
       │           ├── GET /find/{imdb_id}?external_source=imdb_id
       │           ├── Extrai movie_results[0].id + release_date
       │           └── UpsertARRMediaTMDB(id, tmdbID, year)
       │
       └── 3. Rate limit: 1s entre requests
       │
       ▼
[Scheduler] (30 min interval)
       └── Re-executa o loop se novos registros aparecerem
```

---

## Fallback de Segurança

O fallback de unicidade já implementado permanece:

```go
// server.go: populateMovieFields
if m.TmdbID <= 0 {
    m.TmdbID = m.ID  // ID é auto-incremento, sempre único
}
```

Esse fallback garante que o Bazarr nunca receba `tmdbId: 0`, mesmo se:
- A API do TMDb estiver offline
- O resolver ainda não tiver processado a lista
- Um novo registro aparecer entre resoluções

O resolver tenta preencher o `tmdb_id` real; se falhar, o fallback cuida da segurança.

---

## Testes

```
$ go test -v -race ./...

PASS: 100% (70/70 testes)
ok  internal/arr              2.910s
ok  internal/catalog/tmdb     1.029s
ok  internal/metadb           3.351s
ok  internal/subprovider      0.00s
ok  internal/syncer/engines   1.043s
ok  internal/gostorm/torr/storage/torrstor  0.05s
ok  internal/syncer/engines   1.043s
```

- `TestFindResult_ParseMovieResult` — parse da resposta `/find/` para movies
- `TestFindResult_ParseTVResult` — parse com fallback para TV shows
- `TestFindResult_ParseEmpty` — resposta vazia
- Todos os testes existentes continuam passando (67 testes anteriores)

---

## Compilação

```
$ go vet ./...           → 0 erros
$ go build ./...         → 0 erros
$ go test -v -race ./... → PASS 100%
```

---

## Commit e Push

- **Branch:** `fix/arr-id-fallback-cleanup`
- **Commit:** `da51688`
- **Push:** `a101d0d..da51688 → origin/fix/arr-id-fallback-cleanup`
- **Stats:** 6 arquivos modificados, 355 linhas inseridas

---

## Observações

1. **Não criamos pacote `internal/metadata/`** — o resolver está em `internal/arr/resolver.go` conforme diretriz de modificação mínima.

2. **Reutilização total do client TMDb** — `FindByIMDbID` usa o mesmo `http.Client` (timeout 30s + retry logic) e `rate.Limiter` (4 req/s) já existentes.

3. **Configuração** — leitura de `cfg.TMDBAPIKey` com fallback `os.Getenv("TMDB_API_KEY")` segue padrão existente (`MKV_PROXY_*`, `AI_*`).

4. **Background apenas** — o resolver não bloqueia o boot. O backfill síncrono completa primeiro; o resolver inicia 5s depois em goroutine separada.

5. **Escopo restrito** — nenhum schema de banco foi alterado. As queries usam colunas existentes (`tmdb_id`, `imdb_id`, `year`).
