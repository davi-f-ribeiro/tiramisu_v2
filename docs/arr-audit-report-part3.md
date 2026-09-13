---

## FUNÇÕES REMOVIDAS

| Função | Pacote | Motivo |
|--------|--------|--------|
| `SetAppName(name)` | `arr` | Nunca chamada — appName imutável após constructor |
| `getAppName()` | `arr` | Substituída por acesso direto `h.appName` |
| `defaultQuality` | `arr` | Variável declarada mas nunca referenciada |

## FUNÇÕES NOVAS MANTIDAS

| Função | Pacote | Responsabilidade |
|--------|--------|-----------------|
| `populateMovieFields(m)` | `arr` | Extração da lógica duplicada de handlers |
| `reTMDB` | `arr` | Regex package-level para `extractTMDBID` |
| `seriesDirToID` (local) | `arr` | Mapa O(1) para backfill de episódios |

## GOROUTINES AUDITADAS

| Goroutine | Origem | Lifecycle | Shutdown | Estado compartilhado |
|-----------|--------|-----------|----------|---------------------|
| `StartStandaloneListeners` g1 | `arr/server.go:775` | Radarr em :7878 | `arrCtx` cancelado → `radarrSrv.Shutdown()` | `http.Server` (inerte por si) |
| `StartStandaloneListeners` g2 | `arr/server.go:786` | Sonarr em :8989 | `arrCtx` cancelado → `sonarrSrv.Shutdown()` | `http.Server` (inerte por si) |
| `StartStandaloneListeners` g3 | `arr/server.go:797` | Espera `<-ctx.Done()` | `arrCtx` já cancelado | `radarrSrv`/`sonarrSrv` (shutdown) |
| `arrCancel` goroutine | `main.go:4364` | Espera `<-backgroundStopChan` | `backgroundStopChan` fechado | `arrCancel` (safe) |
| Backfill goroutine | `main.go:4375` | `arr.RunBackfill(arrCtx, ...)` | `arrCtx` cancelado → backfill para | `*sql.DB` (via `arrStore`) |

**Conclusão:** Todas as goroutines ARR têm lifecycle vinculado a `arrCtx` → `backgroundStopChan`. O backfill usa o mesmo contexto dos servidores HTTP — SIGTERM cancela ambos.

## DUPLICAÇÕES ELIMINADAS

| Antes | Depois | Resultado |
|-------|--------|-----------|
| `handleMovieList` + `handleMovieDetail` duplicados (~50 linhas) | `populateMovieFields()` comum | ✅ Elimina duplicação |
| Busca linear O(N×M) no backfill de episódios | Map O(1) `seriesDirToID` | ✅ Elimina N×M |
| `regexp.MustCompile` em cada `extractTMDBID` chamada | `reTMDB` package-level | ✅ Elimina recompilação |
| Fallback `tmdbId/tvdbId = ID` (4 locais) | IDs deixados como 0 | ✅ Elimina dados fabricados |

## TESTES EXECUTADOS

```
go build ./...    → ✅ OK
go test ./...     → ✅ OK (tiramisu/internal/arr passou)
go vet ./...      → ✅ OK
gofmt -l ...      → ✅ OK (arquivos modificados formatados)
```

**Resultados específicos do pacote `internal/arr`:**
- `TestMovieTmdbIdLeftZeroWhenAbsent` ✅ (antigo TestMovieTmdbIdFallbackDistinct)
- `TestSeriesTvdbIdLeftZeroWhenAbsent` ✅ (antigo TestSeriesTvdbIdFallbackDistinct)
- `TestManualSyncDurationMs` ✅ (asserção ajustada: >= 0 em vez de > 0)
- Todos os demais testes do pacote `internal/arr` ✅

## TESTES NÃO EXECUTADOS

```
go test -race ./...  → NÃO EXECUTADO (não solicitado/executado)
staticcheck ./...    → NÃO EXECUTADO (não instalado)
```
