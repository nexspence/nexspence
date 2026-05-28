# План: RBAC фильтрация в Search/Components + Path Picker для Docker

**Дата:** 2026-04-21  
**Проблема:** Content selector не ограничивает доступ в Search/Browse; path picker показывает неправильные пути

---

## Диагностика: найденные root causes

### RC-1 — Search и Components не фильтруются по RBAC (главная причина "вижу всё")

`/service/rest/v1/components`, `/service/rest/v1/search`, `/service/rest/v1/search/assets` зарегистрированы в группе `authed` — только JWT-аутентификация, **никакого RBAC**.  
`rbacSvc.FilterPaths` / `FilterDockerRows` вызываются ТОЛЬКО в browse endpoints. ComponentHandler и SearchAssets не знают про content selectors вообще.

```
GET /service/rest/v1/search?repository=docker  →  authed (no RBAC)  →  все компоненты
GET /service/rest/v1/components?repository=docker  →  authed (no RBAC)  →  все компоненты
```

**Следствие:** любой залогиненный пользователь видит все компоненты/assets во всех репозиториях.

---

### RC-2 — Path picker показывал `/da//nta/` (двойной слэш, уже исправлено в коде)

Баг в `dockerImageDirs`: `cur += "/" + seg + "/"` накапливалось неправильно.  
**Исправлено** в текущем коде: `cur += seg + "/"` → `"/" + cur`.  
Но если docker-compose не перебилден — в контейнере работает старый бинарь.

---

### RC-3 — CEL expression может не совпадать с путём в RBAC middleware

Docker-запрос `HEAD /v2/repository/docker/da/dev/python/blobs/sha256:...`  
→ RBAC middleware получает `dockerpath = /da/dev/python/blobs/sha256:...`  
→ content selector `path.startsWith("/da/dev/")` → ✓ матчит  

Но: если content selector был создан **до** исправления двойного слэша, expression в БД может быть `path.startsWith("/da//dev/")` (сломанный) → не матчит никогда.

**Нужно:** удалить старые content selectors и создать заново после деплоя.

---

### RC-4 — Privilege без actions = разрешает всё или ничего?

В `matchPrivileges`:
```go
func actionAllowed(allowed []string, action string) bool {
    if len(allowed) == 0 {
        return true  // пустой список = all actions allowed
    }
```
Если privilege создаётся с пустым `actions: []` — ОК, разрешает всё.  
Если создаётся вообще без поля `attrs` — `actions = nil` → ОК тоже.  
Проблем нет, но нужно проверить что в БД.

---

## Шаги исправления

### Шаг 1 — Перебилдить и задеплоить (устраняет RC-2)

```bash
docker compose down && docker compose up --build -d
```

Проверить что новый бинарь:
```bash
curl -s http://localhost:8081/api/v1/browse/repositories/docker/path-tree \
  -H "Authorization: Bearer <token>" | jq '.paths'
```
Должно вернуть: `["/da/", "/da/dev/", "/da/dev/python/"]` — без `blobs`, `manifests`, без двойных слэшей.

---

### Шаг 2 — Добавить RBAC фильтрацию в ComponentHandler и SearchAssets (устраняет RC-1)

**Файлы для изменения:**

#### `internal/api/handlers/components.go`
- Добавить `rbacSvc *service.RBACService` в `ComponentHandler`
- В `List`: после получения компонентов — вызвать `filterComponentsByRBAC`
- В `Search`: аналогично
- В `SearchAssets`: аналогично

**Логика фильтрации компонентов:**
```go
func filterComponentsByRBAC(
    ctx context.Context,
    rbac *service.RBACService,
    userID string, roles []string,
    repo *domain.Repository,
    items []domain.Component,
) []domain.Component {
    if service.IsAdmin(roles) || repo.AllowAnonymous {
        return items
    }
    privs := rbac.GetPrivilegesForUser(ctx, userID)
    result := items[:0]
    for _, c := range items {
        // Для Docker: samplePath = "/da/dev/python/manifests/latest"
        // Для остальных: samplePath = asset path
        if rbac.MatchPrivileges(privs, repo.Name, c.SamplePath(), "browse") {
            result = append(result, c)
        }
    }
    return result
}
```

**Что нужно добавить в `RBACService`:**
- Экспортировать `IsAdmin(roles []string) bool` (сейчас приватный)
- Экспортировать `MatchPrivileges` или добавить `CanBrowseComponent(ctx, userID, roles, repo, samplePath)`
- Добавить `GetPrivilegesForUser(ctx, userID) ([]PrivilegeWithSelector, error)` чтобы не делать N запросов к БД для каждого компонента

**SamplePath для компонента:**
- Нужно определить какой путь использовать для проверки доступа к компоненту
- Вариант A: использовать `component.Name + "/" + component.Version` → `/da/dev/python/latest`
  - НЕ совпадает с dockerpath (там `/da/dev/python/manifests/latest` или `/da/dev/python/blobs/sha256:...`)
- Вариант B: использовать первый asset path → нужен join с assets в SQL
- Вариант C: для Docker формировать путь как `"/" + name + "/"` → `/da/dev/python/`
  - Простейший вариант: content selector `path.startsWith("/da/dev/")` матчит `/da/dev/python/`

**Рекомендация: Вариант C** — использовать `"/" + component.Name + "/"` как samplePath для компонентов. Это корректно потому что content selector `path.startsWith("/da/dev/")` даёт доступ ко всем образам под этим namespace.

#### `internal/api/router.go`
- Передать `rbacSvc` в `NewComponentHandler`

---

### Шаг 3 — Удалить старые сломанные content selectors и создать заново (устраняет RC-3)

После деплоя:
1. Открыть Security → Content Selectors
2. Удалить все старые (с `/da//dev/` или неправильными expressions)
3. Создать заново через UI — теперь path picker покажет `da/dev` без слэшей
4. Проверить CEL expression в поле preview: должно быть `repository == "docker" && path.startsWith("/da/dev/")`

---

### Шаг 4 — Проверка всей цепочки

#### Проверка path-tree API:
```bash
TOKEN="<jwt-token>"
# Должно вернуть /da/, /da/dev/, /da/dev/python/ — без blobs/manifests
curl -s "http://localhost:8081/api/v1/browse/repositories/docker/path-tree" \
  -H "Authorization: Bearer $TOKEN" | jq '.paths[]'
```

#### Проверка content selector выражения в БД:
```bash
docker exec nexspence-postgres-1 psql -U nexspence -c \
  "SELECT name, expression FROM content_selectors;"
# Expression НЕ должно содержать двойных слэшей
```

#### Проверка RBAC для не-admin пользователя:
```bash
USER_TOKEN="<token-of-non-admin-user>"
# Должен видеть только компоненты своего namespace
curl -s "http://localhost:8081/service/rest/v1/search?repository=docker" \
  -H "Authorization: Bearer $USER_TOKEN" | jq '.items[].name'
```

#### Проверка docker push/pull:
```bash
docker login localhost:8081 -u testuser -p testpassword
docker push localhost:8081/repository/docker/da/dev/python:latest
# Должно работать (если у testuser есть write-privilege на /da/dev/)
docker pull localhost:8081/repository/docker/da/nta/nginx:latest
# Должно вернуть 401 (если у testuser нет доступа к /da/nta/)
```

---

## Порядок реализации

| # | Задача | Файлы | Эффект |
|---|--------|-------|--------|
| 1 | `docker compose up --build` | — | Path picker без двойных слэшей |
| 2 | Пересоздать content selectors в UI | — | Правильные CEL expressions |
| 3 | Экспортировать `IsAdmin`, добавить `CanBrowseComponent` в `RBACService` | `rbac_service.go` | Готовый API для шага 4 |
| 4 | Добавить `rbacSvc` в `ComponentHandler`, фильтровать List/Search/SearchAssets | `components.go`, `router.go` | Search показывает только доступное |
| 5 | Проверка: non-admin видит только свои компоненты, docker push/pull работает | — | End-to-end |

---

## Важные замечания

- **Фильтрация компонентов — read/browse** это только UI-видимость. Реальная защита артефактов (блобов) — через RBACMiddleware на `/v2/repository/` и `/repository/` маршрутах (уже работает).
- **Admin всегда видит всё** — это правильно, `isAdmin(roles)` short-circuits все проверки.
- **AllowAnonymous репозитории** — фильтрация не применяется (открытые репозитории — видны всем).
- **Group repos** — нужно проверять права на каждый member-репозиторий отдельно или на group-имя.
