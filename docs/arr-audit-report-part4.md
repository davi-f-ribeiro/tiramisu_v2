---

## ARQUIVOS MODIFICADOS

| Arquivo | Linhas | Tipo |
|---------|--------|------|
| `internal/arr/server.go` | -121/+60 | ARR-specific |
| `internal/arr/backfill.go` | -11/+13 | ARR-specific |
| `internal/arr/backfill_test.go` | -2/+2 | Teste ARR |
| `internal/arr/server_test.go` | -28/+14 | Teste ARR |
| `internal/syncer/engines/tv_go.go` | -2/+7 | Core integration |
| `main.go` | -5/+7 | Core integration |

**Total:** 6 arquivos, -169/+119 linhas (net -50 linhas)

### Classificação

**ARR-specific (4 arquivos):**
- `internal/arr/server.go` — remoção de fallbacks, código morto, refatoração handlers
- `internal/arr/backfill.go` — O(N×M)→O(N), regex package-level
- `internal/arr/backfill_test.go` — ajuste de asserção
- `internal/arr/server_test.go` — atualização de testes

**Core integration (2 arquivos):**
- `internal/syncer/engines/tv_go.go` — campo metadb removido + cleanup ARR em removeStub
- `main.go` — backfill usa contexto canelável

**Arquivos upstream NÃO modificados:**
- `internal/metadb/db.go` — schema unchanged
- `internal/metadb/arr.go` — unchanged
- `internal/syncer/engines/movie_go.go` — unchanged
- `internal/anacrolix-torrent/` — unchanged
- `internal/gostorm/` — unchanged

### RISCO DE MERGE COM UPSTREAM

| Arquivo | Risco | Motivo |
|---------|-------|--------|
| `internal/syncer/engines/tv_go.go` | BAIXO | Alteração mínima: 1 campo removido + 5 linhas de cleanup |
| `main.go` | BAIXO | Alteração mínima: 1 linha trocada (context.Background → arrCtx) + comentário |
| `internal/arr/` | SEM RISCO | Arquivos novos — não há upstream para conflitar |

**Conflitos prováveis:** Nenhum. As alterações em `tv_go.go` e `main.go` são linhas isoladas, sem reorganização de código.

---

## VERIFICAÇÕES

### API ARR / SignalR — INTACTA
- ✅ Todos os endpoints existentes preservados (v3 + legacy)
- ✅ Handshake SignalR dummy intacto
- ✅ `handleSystemStatus` retorna appName correto (Radarr/Sonarr por porta)
- ✅ `parseQualityFromFilename` preservado
- ✅ Estrutura JSON idêntica — mesmos campos, mesmas respostas
- ✅ `WithAppName("Sonarr")` preservado para listener Sonarr

### Caminhos de criação de stub → ARR
| Caminho | Cria stub? | Registra ARR? | DB usado |
|---------|------------|---------------|----------|
| MovieSyncer → processMovie | ✅ Sim | ✅ `e.metadb.UpsertARRMovie` | `metadb.DB` (stateDB) |
| TVSyncer → processFullpack | ✅ Sim | ✅ `e.db.UpsertARREpisode` | `metadb.DB` (stateDB) |
| TVSyncer → processSingle | ✅ Sim | ✅ `e.db.UpsertARREpisode` | `metadb.DB` (stateDB) |
| Backfill | ✅ Sim | ✅ `RunBackfill` → `Upsert...` | `*sql.DB` (mesmo stateDB) |
| Stub Management API | Via engines | ✅ Mesmos engines | `metadb.DB` (stateDB) |

Todos usam o mesmo `stateDB` — ✅ consistente.

### Caminhos de remoção → ARR cleanup
| Caminho | Remove stub? | Remove ARR? |
|---------|-------------|-------------|
| MovieGoEngine.removeStub | ✅ Via FUSE | ✅ `DeleteARRMediaByPath` |
| TVGoEngine.removeStub | ✅ Via FUSE | ✅ `DeleteARRMediaByPath` (NOVO) |

Agora ambos os engines limpam o catálogo ARR quando um stub é removido. ✅

---

## REGRESSÕES

### Core original — INTACTO
- ✅ FUSE — sem alterações
- ✅ GoStorm — sem alterações
- ✅ Syncers — sem alterações (apenas cleanup ARR em removeStub)
- ✅ TV — sem alterações
- ✅ Movies — sem alterações
- ✅ Stub management — sem alterações
- ✅ MetaDB — sem alterações (schema unchanged)
- ✅ Playback — sem alterações
- ✅ HTTP API original — sem alterações

### API ARR/Bazarr — VALIDADA
- ✅ Contrato HTTP preservado
- ✅ Handshake SignalR preservado
- ✅ Respostas JSON idênticas (IDs externos agora 0 quando ausentes, nunca fabricados)
- ✅ `parseQualityFromFilename` preservado
