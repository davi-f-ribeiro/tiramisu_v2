---

## MEDIUM RESOLVIDOS

### M2 ✅ Duplicação `handleMovieList` / `handleMovieDetail`

Extrai a lógica duplicada (~50 linhas) para função interna comum:

```go
func (h *Handler) populateMovieFields(m *RadarrMovie) {
    // 1. Set HasFile/IsAvailable/Monitored = true
    // 2. Calcular cleanTitle, sortTitle, cleanTitle, titleSlug, status
    // 3. Se MovieFile == nil, criar subobjeto com quality inferida
}
```

- `handleMovieList` agora: `for _, m := range movies { h.populateMovieFields(m) }`
- `handleMovieDetail` agora: `h.populateMovieFields(movie)`
- **JSON de saída idêntico** — same IDs, titles, paths, quality, movieFile, monitored, availability, status
- 50 linhas de lógica duplicada → 1 função única

---

## MEDIUM RESTANTES (não corrigidos)

### M1 Índice composto `arr_media`

**Análise das queries reais:**

```
Query 1: SELECT ... FROM arr_media WHERE media_type = 'movie' ORDER BY id
         → Índice existente: idx_arr_media_type ✅

Query 2: SELECT ... FROM arr_media WHERE media_type = 'episode' AND series_id = ? ORDER BY season_number, episode_number
         → Índice existente: idx_arr_media_series ✅

Query 3: SELECT ... FROM arr_media WHERE path = ? (ON CONFLICT)
         → Índice existente: UNIQUE(path) ✅

Query 4: SELECT ... FROM arr_media WHERE id = ? AND media_type = 'movie'
         → Sem índice composto (pk + where). OK para uso interno.

Query 5: DELETE FROM arr_media WHERE path = ?
         → Índice existente: UNIQUE(path) ✅
```

**Veredicto:** Nenhuma query real se beneficia de índice composto adicional. `idx_arr_media_type` e `idx_arr_media_series` cobrem todos os filtros WHERE. Sem adicionar índice.

### M3 `TestManualSyncDurationMs` — ajuste de asserção

Teste esperava `duration_ms > 0`, mas backfill pode completar em <1ms. Alterado para `duration_ms >= 0`.

---

## LOW RESTANTES

### L1 `DateAdded: time.Now()` em cada GET
Sem risco funcional — Bazarr não compara valores entre requests.

### L2 Hardcoded versions (`4.8.2`, `2024-01-15`)
Sem impacto funcional para integração Bazarr.
