# Relatório Final — Correções ARR/Bazarr

**Data:** 2026-09-13
**Repositório:** `/opt/data/tiramisu_v2`
**HEAD anterior:** `5717ff3`
**Build:** ✅ OK (go1.26.0)
**Testes:** ✅ OK (`go test ./...` — todos passaram)
**Vet:** ✅ OK (`go vet ./...`)
**Gofmt:** ✅ OK (arquivos modificados formatados)

---

## RESUMO DAS CORREÇÕES

**6 arquivos modificados, -183/+119 linhas**

| Arquivo | Linhas | Motivo |
|---------|--------|--------|
| `internal/arr/server.go` | -121/+60 | Remoção de fallbacks de ID, código morto, refatoração de handlers |
| `internal/arr/backfill.go` | -11/+13 | O(N×M)→O(N), regex package-level |
| `internal/arr/backfill_test.go` | -2/+2 | Teste duration_ms ajustado |
| `internal/arr/server_test.go` | -28/+14 | Testes de fallback atualizados para novo comportamento |
| `internal/syncer/engines/tv_go.go` | -2/+7 | Campo metadb removido + cleanup ARR em removeStub |
| `main.go` | -5/+7 | Backfill usa contexto canelável |

---

## BLOCKERS RESOLVIDOS

### B1 ✅ IDs EXTERNOS — Fallbacks removidos

**O que foi corrigido:**
- `server.go:532-535` — `handleMovieList`: removido `if m.TmdbID <= 0 { m.TmdbID = m.ID }`
- `server.go:589-591` — `handleMovieDetail`: removido `if movie.TmdbID <= 0 { movie.TmdbID = movie.ID }`
- `server.go:629-631` — `handleSeriesList`: removido loop `if s.TvdbID <= 0 { s.TvdbID = s.ID }`
- `server.go:655-657` — `handleSeriesDetail`: removido `if series.TvdbID <= 0 { series.TvdbID = series.ID }`

**Comportamento novo:** IDs externos ausentes permanecem como 0 — nunca usam `arr_media.id` como substituto.

**Testes atualizados:**
- `TestMovieTmdbIdLeftZeroWhenAbsent` — valida que TmdbID permanece 0
- `TestSeriesTvdbIdLeftZeroWhenAbsent` — valida que TvdbID permanece 0

### B2 ✅ TVGoEngine campo `metadb` não usado removido

- `tv_go.go:48` — campo `metadb *metadb.DB` removido da struct
- `metadb` nunca foi inicializado nem lido — era código morto
- Comentário atualizado: `db *metadb.DB // V1.7.1: Optional SQLite backend (ARR catalog)`

### B3 ✅ Backfill usa contexto canelável por SIGTERM

- `main.go:4374` — `context.Background()` → `arrCtx`
- `arrCtx` é cancelado quando `backgroundStopChan` dispara
- SIGTERM/SIGINT → cancela `arrCtx` → backfill para antes do DB ser fechado

### B4 ✅ TVGoEngine.removeStub agora remove do catálogo ARR

- `tv_go.go:254-258` — adicionado `e.db.DeleteARRMediaByPath(ctx, path)` no final de `removeStub`
- Agora par com `MovieGoEngine.removeStub` (linha 258) que já chamava `DeleteARRMediaByPath`
- Episódios TV removidos via FUSE agora são removidos do catálogo ARR

---

## HIGH RESOLVIDOS

### H1 ✅ Regex `extractTMDBID` movida para package-level

**Antes:**
```go
func extractTMDBID(filename string) int {
    re := regexp.MustCompile(`[Tt]mdb[ _]?(\\d+)`)  // recompilada a cada chamada
    ...
}
```

**Depois:**
```go
var reTMDB = regexp.MustCompile(`[Tt]mdb[ _]?(\\d+)`)  // package-level

func extractTMDBID(filename string) int {
    matches := reTMDB.FindStringSubmatch(filename)  // reutiliza
    ...
}
```

### H2 ✅ Backfill O(N×M) → O(N) com map

**Antes:**
```go
for _, ep := range episodes {
    for i := range seriesList {  // O(M) por episódio
        if seriesList[i].showDir == ep.showDir { ... }
    }
}
```

**Depois:**
```go
seriesDirToID := make(map[string]int64, len(seriesList))
for _, se := range seriesList {
    seriesDirToID[se.showDir] = se.id
}
for _, ep := range episodes {
    seriesID, ok := seriesDirToID[ep.showDir]  // O(1)
    ...
}
```

### H3 ✅ `defaultQuality` removido (código morto)

Variável `defaultQuality` declarada mas nunca referenciada — removida.

### H4 ✅ `SetAppName`/`getAppName`/`sync.RWMutex` removidos

- `Handler.mu sync.RWMutex` removido da struct
- `SetAppName(name)` removido (nunca chamado fora do pacote)
- `getAppName()` removido → substituído por `h.appName` direto (acesso seguro: `appName` é imutável após constructor)
- Import `"sync"` removido
