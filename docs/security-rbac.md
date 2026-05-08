# Nexspence — Security: Content Selectors, Privileges, Roles

## Концепция RBAC

```
Content Selector  ──►  Privilege  ──►  Role  ──►  User
   (CEL фильтр)       (разрешение)   (набор прав)
```

1. **Content Selector** — CEL-выражение, описывающее *какие* артефакты попадают под правило (формат, путь, репозиторий).
2. **Privilege** — разрешение типа `repository-content-selector`, которое всегда привязано к Content Selector. Это **единственный способ создания привилегий** в Nexspence (в отличие от Nexus, где есть wildcard/application/script).
3. **Role** — набор привилегий. Назначается пользователям.

---

## Тип привилегий

В Nexspence поддерживается единственный тип привилегий, создаваемых через UI:

| Type | Описание |
|------|----------|
| `repository-content-selector` | Привилегия, область действия которой определяется CEL-выражением Content Selector |

Встроенные (built-in) привилегии могут иметь исторические типы (`wildcard`, `repository-view` и др.), но создавать новые привилегии этих типов через UI нельзя.

> **Почему так?** Nexspence упрощает модель доступа: вместо двух шагов (создать привилегию + прикрепить Content Selector) — один шаг: выбрать Content Selector при создании привилегии.

---

## CEL-выражения для Content Selector

Content Selector использует [CEL (Common Expression Language)](https://github.com/google/cel-spec).

### Доступные переменные

| Переменная | Тип | Описание |
|------------|-----|----------|
| `format` | string | Формат репозитория: `"maven2"`, `"npm"`, `"docker"`, `"pypi"`, `"raw"`, `"helm"`, `"cargo"`, `"go"`, `"nuget"`, `"apt"`, `"yum"`, `"conan"` |
| `path` | string | Путь артефакта (начинается с `/`) |
| `repository` | string | Имя репозитория |

### Примеры выражений по форматам

```cel
# Только Maven
format == "maven2"

# Только npm
format == "npm"

# Только Docker
format == "docker"

# PyPI или npm (monorepo-стиль)
format == "pypi" || format == "npm"

# Конкретный репозиторий
repository == "releases"

# Maven из конкретного репозитория
format == "maven2" && repository == "maven-releases"

# Только SNAPSHOT-артефакты Maven
format == "maven2" && path.contains("-SNAPSHOT")

# Helm-чарты из Production-репозитория
format == "helm" && repository == "helm-prod"

# Артефакты группы org.example (Maven)
format == "maven2" && path.startsWith("/org/example/")

# Docker-образы конкретного namespace
format == "docker" && path.startsWith("/v2/myteam/")

# Все артефакты (нет ограничений)
true

# Только публичные (не SNAPSHOT) релизы Maven
format == "maven2" && !path.contains("SNAPSHOT") && !path.contains("-beta")
```

---

## Пошаговая инструкция

### Шаг 1 — Создать Content Selector (если нужна фильтрация)

**UI:** Security → Content Selectors → New Content Selector

**API:**
```http
POST /service/rest/v1/security/content-selectors
Content-Type: application/json

{
  "name": "maven-releases-only",
  "description": "Maven release artifacts (no snapshots)",
  "expression": "format == \"maven2\" && !path.contains(\"SNAPSHOT\")"
}
```

Ответ содержит `id` — он нужен на шаге 3.

---

### Шаг 2 — Создать Privilege

**UI:** Security → Privileges → New Privilege → выбрать Content Selector из списка.  
CEL-выражение выбранного селектора отображается сразу под dropdown.

**API:**
```http
POST /service/rest/v1/security/privileges
Content-Type: application/json

{
  "name": "view-maven-releases",
  "description": "Read Maven release artifacts",
  "type": "repository-content-selector",
  "contentSelectorId": "<selector-uuid>"
}
```

Ответ содержит `id` — нужен для шага 3.

---

### Шаг 3 — Создать Role и назначить Privilege

**UI:** Security → Roles → Edit → добавить привилегии через чекбоксы.

**API — создать роль:**
```http
POST /service/rest/v1/security/roles
Content-Type: application/json

{
  "name": "maven-reader",
  "description": "Read-only access to Maven releases"
}
```

**API — назначить привилегии роли:**
```http
PUT /service/rest/v1/security/roles/{roleId}/privileges
Content-Type: application/json

{
  "privilegeIds": ["<privilege-id-1>", "<privilege-id-2>"]
}
```

---

### Шаг 4 — Назначить Role пользователю

**UI:** Users & Roles → Users → кнопка «Assign Roles»

**API:**
```http
PUT /service/rest/v1/security/users/{userId}/roles
Content-Type: application/json

{
  "roleIds": ["<role-id>"]
}
```

---

## Готовые сценарии по форматам

### Maven — Read-only для конкретного репозитория

```json
// 1. Content Selector
{ "name": "maven-releases", "expression": "format == \"maven2\" && repository == \"maven-releases\"" }

// 2. Privilege (ссылается на selector по id)
{ "name": "nx-maven-releases-read", "type": "repository-content-selector", "contentSelectorId": "<selector-uuid>" }

// 3. Role
{ "name": "maven-developer", "description": "Read Maven releases" }
```

---

### npm — Публикация пакетов (CI/CD)

```json
// 1. Content Selector
{ "name": "npm-all", "expression": "format == \"npm\"" }

// 2. Privilege
{ "name": "nx-npm-all-write", "type": "repository-content-selector", "contentSelectorId": "<selector-uuid>" }

// 3. Role
{ "name": "npm-publisher", "description": "Publish npm packages" }
```

---

### Docker — Только чтение образов своей команды

```json
// 1. Content Selector
{ "name": "docker-myteam", "expression": "format == \"docker\" && path.startsWith(\"/v2/myteam/\")" }

// 2. Privilege
{ "name": "nx-docker-myteam-read", "type": "repository-content-selector", "contentSelectorId": "<selector-uuid>" }

// 3. Role
{ "name": "docker-consumer-myteam" }
```

---

### PyPI — Upload для poetry/twine

```json
// 1. Content Selector
{ "name": "pypi-hosted", "expression": "format == \"pypi\" && repository == \"pypi-hosted\"" }

// 2. Privilege
{ "name": "nx-pypi-write", "type": "repository-content-selector", "contentSelectorId": "<selector-uuid>" }

// 3. Role
{ "name": "pypi-publisher" }
```

---

### Helm — Только чтение для деплоя

```json
// 1. Content Selector
{ "name": "helm-all", "expression": "format == \"helm\"" }

// 2. Privilege
{ "name": "nx-helm-read", "type": "repository-content-selector", "contentSelectorId": "<selector-uuid>" }

// 3. Role
{ "name": "helm-deployer" }
```

---

### Универсальная read-only роль (все форматы)

```json
// 1. Content Selector
{ "name": "all-artifacts", "expression": "true" }

// 2. Privilege
{ "name": "nx-all-read", "type": "repository-content-selector", "contentSelectorId": "<selector-uuid>" }

// 3. Role
{ "name": "anonymous-reader", "description": "Read all public repos" }
```

---

### Maven SNAPSHOT-артефакты запрещены

```json
// 1. Content Selector (только релизы, без SNAPSHOT)
{ "name": "maven-releases-only", "expression": "format == \"maven2\" && !path.contains(\"SNAPSHOT\")" }

// 2. Privilege
{ "name": "nx-maven-no-snapshot", "type": "repository-content-selector", "contentSelectorId": "<selector-uuid>" }

// 3. Role
{ "name": "maven-release-reader" }
```

---

## Встроенные роли (readOnly: true)

| Роль | Описание |
|------|----------|
| `nx-admin` | Полный доступ ко всему |
| `nx-anonymous` | Анонимный пользователь (read-only public repos) |
| `nx-developer` | Чтение + publish артефактов |

Встроенные роли нельзя удалить или изменить.

---

## Порядок проверки доступа

При запросе к артефакту проверяется:

```
1. Аутентификация (JWT Bearer / API Token / Basic Auth)
2. Роли пользователя → список привилегий
3. Для каждой привилегии типа repository-content-selector:
   a. Получить связанный Content Selector → CEL-выражение
   b. Вычислить выражение по переменным (format, path, repository)
   c. Если true → доступ разрешён
4. Хотя бы одна привилегия даёт доступ → OK
```

---

## LDAP External Role Mapping

При каждом логине LDAP-пользователя Nexspence автоматически синхронизирует его роли из группового членства LDAP (REPLACE-семантика — старые роли заменяются).

### Стратегии маппинга (применяются все три)

| Приоритет | Стратегия | Конфиг |
|-----------|----------|--------|
| 1 | `admin_group` → `nx-admin` | `ldap.admin_group` |
| 2 | Явный маппинг group → role | `ldap.role_mappings` |
| 3 | Имя группы = имя роли | автоматически |

### Конфигурация

```yaml
ldap:
  enabled: true
  admin_group: "nexus-administrators"   # plain CN или полный DN
  role_mappings:
    "dev-team":  "developers"   # LDAP group → Nexspence role name
    "qa-team":   "testers"
    "ops-group": "operators"
```

### Поведение

- Роли назначаются через `SetUserRoles` (REPLACE): на каждом логине список ролей пользователя полностью пересчитывается из его текущих LDAP-групп.
- Если LDAP-группа не найдена среди ролей ни одной из стратегий — она игнорируется (не создаёт новых ролей).
- `admin_group` принимает как plain CN (`"nexus-administrators"`), так и полный DN (`"CN=nexus-administrators,OU=Groups,DC=example,DC=com"`) — первый RDN сравнивается case-insensitive.
- Ошибка синхронизации ролей не блокирует вход (best-effort, логируется).

---

## Troubleshooting

| Проблема | Причина | Решение |
|----------|---------|---------|
| 403 при чтении артефакта | Нет привилегии, чей Content Selector matches запрос | Проверить CEL-выражение — убедиться что `format`, `repository`, `path` совпадают |
| Content Selector не работает | CEL-выражение ошибочное | GET /service/rest/v1/security/content-selectors — проверить поле `expression` |
| Роль назначена, но нет доступа | Privilege не прикреплена к Role | PUT /roles/{id}/privileges с нужными IDs |
| Docker pull denied | Docker использует `/v2/...` пути | Content Selector с `format == "docker"` |
| "No content selectors defined" в UI | Сначала нужно создать Content Selector | Security → Content Selectors → New Selector |
