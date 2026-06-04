# Nexspence — бесплатная альтернатива Nexus Repository, которую помогал мне писать Claude Code

Привет, Хабр! Примерно полтора года назад я решил обновить наш рабочий Nexus Repository OSS — и был «приятно» удивлён: Sonatype отказались от OSS-версии и перешли на Community Edition с лимитами на количество репозиториев и компонентов. Платить за Pro я не хотел, переходить на Artifactory тоже — JFrog ушёл из России ещё в 2022-м. Так что пообещал себе разобраться с этим «на следующих выходных» и написать что-то своё. Выходные закончились, а я всё ещё пишу код 🙂 Но зато получился нормальный инструмент, которым пользуемся сами — и теперь хочу рассказать про него.

Это статья про **Nexspence** — open-source менеджер артефактов на Go, который поддерживает 14 форматов пакетов и совместим с Nexus REST API.

---

## Содержание

1. [О проекте](#о-проекте)
2. [Зачем писать своё](#зачем-писать-своё)
3. [Стек и архитектура](#стек-и-архитектура)
4. [Что умеет](#что-умеет)
5. [Quick Start](#quick-start)
6. [Чего пока нет](#чего-пока-нет)
7. [Ссылки](#ссылки)

---

## О проекте

**Nexspence** — это self-hosted менеджер артефактов: Maven, npm, PyPI, Docker, Helm, Go modules и ещё 8 форматов в одном сервисе. Работает как одним Docker-контейнером, так и в Kubernetes через Helm chart. Лицензия AGPLv3, код полностью открыт.

Текущая версия — **v1.8.2**, с момента первого релиза вышло 19 версий.

- Сайт: [nexspence.com](https://nexspence.com)
- Релизы: [github.com/nexspence/nexspence/releases](https://github.com/nexspence/nexspence/releases)
- nxs (cli инструмент для Nexspence): [github.com/nexspence/nxs](https://github.com/nexspence/nxs)

---

## Зачем писать своё

Nexus Repository OSS бесплатный, но в нём нет HA, нет staging-продвижения, репликация только в Pro. Artifactory — платный, JFrog с 2022-го из России ушёл, а потребность в инструменте никуда не делась.

Я хотел несколько простых вещей:

- Поднять одной командой (`docker compose up`) без лицензий и регистраций
- Получить совместимость с уже существующими CI/CD — поменял URL и поехал
- Нормальный UI, а не то, что было в Nexus 2.x
- Без ограничений типа «эта фича только в Pro»

Первые два месяца я писал «просто прокси для Maven». Потом добавил npm, потом Docker понадобился коллеге, потом Helm — и в итоге я понял, что если делать, то делать нормально.

---

## Стек и архитектура

**Backend — Go.** Gin для HTTP, pgx для PostgreSQL, golang-migrate для схемы БД. Собирается в один статический бинарь — никакой JVM, никакого classpath hell. Nexus в покое потребляет 1.5–2 ГБ RAM, Nexspence — ~60–80 МБ.

**Frontend — React + TypeScript + Vite.** Zustand, React Query, тёмная glassmorphism-тема. Честно говоря, на CSS я потратил больше времени, чем хотел бы признавать 😅

**База — PostgreSQL.** Все метаданные (компоненты, ассеты, пользователи, роли, аудит) живут там. Блобы — на локальной ФС или в S3-совместимом хранилище (MinIO, Ceph).

Миграции запускаются автоматически при старте — поднял контейнер, всё готово, никаких отдельных шагов.

Главное архитектурное решение, которое я принял в самом начале: **полная совместимость с Nexus REST API**. `/service/rest/v1/` работает так же, как у Nexus — CI/CD, скрипты, утилиты продолжают работать без изменений, нужно только поменять хост.

---

## Что умеет

### 14 форматов пакетов

| Формат | Hosted | Proxy | Group |
|--------|:------:|:-----:|:-----:|
| Maven 2/3 | ✓ | ✓ | ✓ |
| npm | ✓ | ✓ | ✓ |
| PyPI | ✓ | ✓ | ✓ |
| Docker (OCI v2) | ✓ | ✓ | ✓ |
| Go modules | ✓ | ✓ | ✓ |
| NuGet v3 | ✓ | ✓ | ✓ |
| Helm | ✓ | ✓ | ✓ |
| Cargo | ✓ | ✓ | ✓ |
| Conda | ✓ | ✓ | — |
| Terraform | ✓ | ✓ | — |
| Raw | ✓ | — | ✓ |
| Apt | ✓ | ✓ | — |
| Yum/RPM | ✓ | ✓ | — |
| Conan | ✓ | ✓ | — |

**Proxy** кеширует артефакты при первом обращении — CI больше не зависит от аптайма внешних реестров. **Group** объединяет несколько репозиториев под одним URL.

### RBAC с Content Selectors

Вместо доступа «ко всему репозиторию» можно написать CEL-выражение:

```
path =~ "^/com/mycompany/.*" && format == "maven2"
```

Это Content Selector. Из него создаётся Privilege, Privilege кладётся в Role, Role назначается пользователю. Разработчик пушит только в свой namespace, не видя чужого.

### SSO — OIDC и LDAP

Keycloak, Google Workspace, Microsoft Entra подключаются через Authorization Code Flow + PKCE. При первом логине пользователь создаётся автоматически, роли маппятся из групп IdP. LDAP тоже есть — для тех, у кого Active Directory.

### Аудит-лог

Каждое действие — логин, публикация артефакта, изменение роли — пишется в `audit_events`. Стриминговый экспорт в NDJSON с фильтрами по дате и пользователю. Это была реальная потребность: «кто и когда удалил этот пакет?»

### Политики очистки

Удалять ассеты старше N дней, оставлять K последних версий, запускать по cron. Есть dry-run — сначала смотришь что попадёт под удаление, потом подтверждаешь.

### Миграция с Nexus

Встроенный инструмент миграции вытягивает через REST API репозитории, пользователей, политики и все артефакты стримингом с паузой/возобновлением. Ни один JAR не пострадает 🙂

---

## Quick Start

Клонировать репозиторий не нужно — достаточно скачать zip с релиза.

**1. Скачиваем и распаковываем**

Берём последний релиз со страницы [github.com/nexspence/nexspence/releases](https://github.com/nexspence/nexspence/releases):

```bash
# Скачиваем архив
curl -LO https://github.com/nexspence/nexspence/releases/download/v1.8.2/nexspence-v1.8.2.zip

# Распаковываем
unzip nexspence-v1.8.2.zip -d nexspence && cd nexspence
```

**2. Настраиваем конфиг**

```bash
cp config.yaml.example config.yaml
# Открываем config.yaml и меняем пароль администратора и JWT-секрет
```

**3. Запускаем**

```bash
docker compose up -d
```

Через несколько секунд UI доступен на `http://localhost:8081`, логин `admin` / пароль из `config.yaml`.

Если нужен HA-режим (2 ноды + nginx):

```bash
docker compose -f docker-compose.ha.yml up -d
# UI: http://localhost:8080
```

---

**Maven:**

```xml
<!-- pom.xml -->
<distributionManagement>
  <repository>
    <id>nexspence</id>
    <url>http://localhost:8081/repository/maven-releases/</url>
  </repository>
</distributionManagement>
```

```bash
mvn deploy -s settings.xml
```

**Docker:**

```bash
docker login localhost:8081 -u admin -p ваш_пароль
docker tag myapp:latest localhost:8081/repository/docker-hosted/myapp:latest
docker push localhost:8081/repository/docker-hosted/myapp:latest
```

В архиве также лежат скрипты `scripts/seed-all.sh` — они создадут 42 тестовых репозитория, тестовые пакеты и RBAC-окружения для dev/stage/prod, чтобы сразу пощупать всё вживую.

Для работы из терминала и CI/CD есть отдельная утилита [`nxs`](https://github.com/nexspence/nxs) — умеет публиковать, скачивать и листить компоненты прямо из командной строки или GitHub Actions.

---

## Чего пока нет

Буду честен. В v1.8.2 нет полноценного HA с лидер-выборами (PostgreSQL + S3 дают shared state, но координацию между нодами не делал). Trivy-сканирование есть, но без scheduler — запускается вручную или через API. Нет layer viewer для Docker-образов в UI.

Это открытые задачи — если что-то из этого нужно, открывайте issues.

---

## Ссылки

- 🌐 Сайт: [nexspence.com](https://nexspence.com)
- 💻 GitHub: [github.com/nexspence/nexspence](https://github.com/nexspence/nexspence)
- 📦 Релизы: [github.com/nexspence/nexspence/releases](https://github.com/nexspence/nexspence/releases)
- 🔧 CLI `nxs`: [github.com/nexspence/nxs](https://github.com/nexspence/nxs)

Если попробуете — пишите в комментариях как впечатления. Особенно интересно слышать от тех, кто мигрирует с Nexus или ищет альтернативу Artifactory.
