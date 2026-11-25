# Nexspence v2.13 — бесплатная альтернатива Nexus Repository, которую помогал мне писать Claude Code

Привет, Хабр!

Полтора года назад я писал пост о том, что Sonatype закрыла OSS-версию Nexus Repository и пришлось заводить свой менеджер артефактов. Тогда это была пара выходных и очень ранний прототип. С тех пор вышло **19 минорных релизов**, продукт стал self-hosted-решением, которым мы пользуемся сами, и — главное — им начали пользоваться другие. Сейчас на дворе **v2.13.5**, и это хороший повод рассказать, что получилось.

Коротко о главном:

- **AGPL-3.0-or-later**, один бинарник, PostgreSQL и blob-хранилище. Никаких лицензий, никаких платных уровней, никакой телеметрии.
- **19 форматов** пакетов: Maven, npm, PyPI, Docker/OCI, Go modules, NuGet, Helm, Cargo, Raw, APT, Yum/RPM, Conan, Conda, Terraform Registry, RubyGems, R/CRAN, Alpine, Hugging Face.
- **Полная совместимость с Nexus REST API v1** — для CI обычно меняется только хост.
- **Живая миграция с работающего Nexus** — репозитории, артефакты, пользователи, роли, routing rules.
- **Supply-chain security в комплекте**: SBOM и подписи (в работе, релиз 3.0.0), pull-гейт по severity, минимальный возраст пакета, гейт сканирования при промоушене.

Сайт: [nexspence.com](https://nexspence.com) · Репозиторий: [github.com/nexspence/nexspence](https://github.com/nexspence/nexspence)

---

## Содержание

1. [Почему Nexus CE больше не вариант](#почему-nexus-ce-больше-не-вариант)
2. [Nexus CE против Nexspence](#nexus-ce-против-nexspence)
3. [19 форматов](#19-форматов)
4. [Что изменилось с прошлой статьи](#что-изменилось-с-прошлой-статьи)
5. [Supply-chain security](#supply-chain-security)
6. [Миграция с Nexus](#миграция-с-nexus)
7. [Quick Start](#quick-start)
8. [Чего пока нет](#чего-пока-нет)
9. [Ссылки](#ссылки)

---

## Почему Nexus CE больше не вариант

Три вещи, которые вышли из Nexus Repository Community Edition и которые ломают его использование по назначению:

**Лицензия.** С версии 3.77.0 исходный код Nexus Repository — проприетарный EULA, и «open-source core» покрывает только Maven, raw и APT. Тот самый Nexus, который годами ставили «просто чтобы работало», теперь требует лицензию.

**Лимиты.** 40 000 компонентов и 100 000 запросов в сутки. Для команды разработки это упирается в потолок быстрее, чем кажется: считаются все обращения к прокси-репозиториям, а не только записи.

**Телеметрия.** С версии 3.97.0 (октябрь 2026) телеметрия обязательна, её надо подтверждать при установке, а инстанс, который не может отчитаться 10 дней (30 начиная с 3.98), переходит в read-only. Исключения для Community Edition нет.

Плюс к этому JFrog Artifactory ушёл из России ещё в 2022-м, а платные уровни Nexus (HA, SSO, replication, staging) стоят отдельных денег. Дальше вопрос стоит так: чем заменить.

Мой ответ: ничем не заменять, а написать своё — и не «для галочки», а так, чтобы этим можно было пользоваться в проде. Что и вышло.

---

## Nexus CE против Nexspence

| | Nexus Repository CE | Nexspence |
|---|---|---|
| Лицензия | EULA (проприетарная) с 3.77.0 | AGPL-3.0-or-later, одна лицензия, без уровней |
| Лимит компонентов | 40 000 | нет |
| Лимит запросов | 100 000 в сутки | нет |
| Телеметрия | обязательная с 3.97.0, без исключений для CE | нет |
| High availability | платные уровни | включено — stateless-ноды, Redis-локи, `/healthz` и `/readyz` |
| SSO | в CE только LDAP, OIDC/SAML — в платных | OIDC/OAuth2 **и** LDAP, включено |
| Blob-хранилища | S3 / Azure / GCS — в платных | S3-совместимые (AWS, MinIO, Ceph) и Azure, включено |
| Staging и promotion | платные уровни | включено — CEL-фильтры, гейт сканирования, очередь на подтверждение |
| Рантайм | JVM, ~1.5–2 ГБ RAM | один Go-бинарник, ~60–80 МБ RAM |
| Совместимость API | — | полный Nexus v1 REST API на `/service/rest/v1/` — меняешь хост, а не CI |
| Миграция с Nexus | экспорт/импорт по репозиториям | живой импорт работающего инстанса — репозитории, артефакты, образы, пользователи, роли, routing rules |
| Стоимость | бесплатный уровень + Pro / Pro+ / Enterprise | бесплатно навсегда, живёт на спонсорах |

<sub>Источники: [Nexus Repository editions and feature availability](https://help.sonatype.com/en/nexus-repository-tiers-and-feature-availability.html), [Nexus Repository telemetry](https://help.sonatype.com/en/nexus-repository-telemetry.html), [2025 release notes](https://help.sonatype.com/en/nexus-repository-2025-release-notes.html), [Introducing the Community Edition](https://www.sonatype.com/blog/sonatype-nexus-repository-community-edition). Лимиты и уровни меняются с каждым релизом Sonatype — смотрите ссылки на актуальное состояние. «Sonatype» и «Nexus Repository» — товарные знаки Sonatype, Inc.; Nexspence не связан с Sonatype и не одобрен ими.</sub>

Честно про остальной рынок: **JFrog Artifactory** (бесплатная OSS-версия покрывает только Maven/Gradle/Ivy/SBT, остальное — по числу пользователей), **Pulp** (GPL, многоформатный, плагиновый, за спиной Red Hat), **Harbor** (только OCI-реестр), **Verdaccio** (только npm), **Reposilite** (только Maven). У Nexspence нет задачи обойти всех — он для тех, кому нужен именно Nexus-совместимый API и много форматов в одном месте.

---

## 19 форматов

| Формат | Hosted | Proxy | Group |
|--------|:------:|:-----:|:-----:|
| Maven 2 / 3 | ✓ | ✓ | ✓ |
| npm | ✓ | ✓ | ✓ |
| PyPI | ✓ | ✓ | ✓ |
| Go modules (GOPROXY v2) | ✓ | ✓ | ✓ |
| Docker | ✓ | ✓ | ✓ |
| OCI-артефакты (Helm `oci://`, ORAS, cosign) | ✓ | ✓ | ✓ |
| NuGet v2 / v3 | ✓ | ✓ | ✓ |
| Helm-чарты | ✓ | ✓ | ✓ |
| Cargo (Rust) | ✓ | ✓ | ✓ |
| Raw-файлы | ✓ | ✓ | ✓ |
| APT (Debian/Ubuntu) | ✓ | ✓ | — |
| Yum / RPM | ✓ | ✓ | — |
| Conan (C/C++) | ✓ | ✓ | — |
| Conda | ✓ | ✓ | ✓ |
| Terraform Registry | ✓ | ✓ | — |
| RubyGems | ✓ | ✓ | — |
| R / CRAN | ✓ | ✓ | ✓ |
| Alpine (apk) | ✓ | ✓ | ✓ |
| Hugging Face (модели, датасеты, spaces) | ✓ | ✓ | ✓ |

**Proxy** кеширует артефакты при первом обращении — CI больше не зависит от аптайма внешних реестров. **Group** объединяет несколько репозиториев под одним URL. Три типа репозиториев на каждый формат — это то, что в Nexus CE осталось только у части форматов.

Для 17 форматов из 19 есть **полный гайд**: создание репозиториев, настройка клиента, публикация, проверка загрузки, аутентификация и разбор типичных поломок. Гайды лежат в репозитории, и для 17 форматов из 19 идёт свой `scripts/create-<формат>-repos.sh`, который создаёт репозитории через API одной командой.

---

## Что изменилось с прошлой статьи

Прошлый пост писался про v1.8.2. С тех пор проект вырос из прототипа в нормальный продукт:

- **HA из коробки.** `docker compose -f docker-compose.ha.yml up -d` — две ноды за nginx, stateless-приложение, общее состояние в PostgreSQL и blob-хранилище, Redis для блокировок. Плюс Helm-чарт.
- **Terraform-провайдер.** Репозитории, пользователи, роли и blob-хранилища описываются в HCL и применяются из контура `terraform apply`.
- **SSO расширен.** К OIDC добавился SAML, роли маппятся из групп IdP, есть готовый realm для Keycloak в комплекте.
- **Репликация, вебхуки, трассировка.** Контент можно реплицировать на другой инстанс, события отдавать в вебхуки, а запросы — трассировать через OpenTelemetry.
- **Гейты supply-chain.** Pull-гейт по severity, минимальный возраст пакета на прокси, гейт сканирования при промоушене, write-policy (allow / allow-once / deny).
- **Сканеры.** Trivy для docker/oci, OSV.dev для maven/npm/pypi/cargo.
- **Управление доступом по содержимому.** Privilege может описываться CEL-выражением — `path =~ "^/com/mycompany/.*" && format == "maven2"`, — так что разработчик пушит только в свой namespace и не видит чужого.
- **Аудит.** Каждое действие — логин, публикация, изменение роли — пишется в `audit_events`, экспорт в NDJSON с фильтрами по дате и пользователю.
- **Политики очистки.** Удалять ассеты старше N дней, оставлять K последних версий, запускать по cron. Есть dry-run.
- **Сайт и документация.** Отдельный docs-сайт с переключателем версий и ченджлогом по каждому релизу, 17 подробных гайдов по форматам, интерактивная схема архитектуры.
- **Качество.** 4100+ тестов, покрытие пакета не ниже 80 % на CI, `golangci-lint`, `govulncheck` — всё зелёное на каждом PR.

Разработка идёт при участии Claude Code — отсюда тесты на каждый крайний случай и довольно длинная история коммитов. Метафора не «я писал код, а ИИ писал за меня», а скорее «я ставил задачу и ревьюил». Всё, что попадает в `main`, проходит CI и ревью человека.

---

## Supply-chain security

Это то, чего в Nexus CE нет вообще и что в Nexus Repository Pro стоит отдельных денег. Коротко, что уже работает:

- **Referrers API** для OCI — к образу можно привязать SBOM, подпись, provenance, и они лежат рядом с ним по digest.
- **Pull-гейт по severity** — репозиторий может не отдавать образ, у которого есть уязвимость выше заданного порога.
- **Минимальный возраст пакета на прокси** — свежие релизы из внешнего реестра не попадают к вам сразу, только когда «настоятся». Защита от typosquatting и от скомпрометированных публикаций.
- **Гейт сканирования при промоушене** — артефакт не переедет из staging в production, пока сканер не скажет, что всё чисто.
- **Staging с CEL-фильтрами** — что именно едет в production, решается выражением, а не руками.

В **3.0.0** (breaking-релиз, уже в работе) добавляется генерация SBOM on-demand и локальная подпись, совместимая с cosign, — с публикацией referrer'ов по subject digest. Формат релиза будет чисто breaking: можно ломать API и контракты и вычищать легаси.

---

## Миграция с Nexus

Встроенный инструмент подключается к работающему инстансу Nexus по REST API и вытягивает репозитории, пользователей, роли, политики и артефакты — стримингом, с паузой и возобновлением. Ни один JAR не пострадает 🙂

Дальше переключаете хост в CI и в клиентах пакетов. Для большинства форматов это ровно одна переменная.

---

## Quick Start

Клонировать репозиторий не нужно — достаточно скачать zip с релиза.

**1. Скачиваем и распаковываем**

Берём последний релиз со страницы [github.com/nexspence/nexspence/releases](https://github.com/nexspence/nexspence/releases):

```bash
# Скачиваем сборку
curl -LO https://github.com/nexspence/nexspence/releases/download/v2.13.5/nexspence-run-essentials-2.13.5.zip

# Распаковываем
unzip nexspence-run-essentials-2.13.5.zip -d nexspence && cd nexspence
```

В архиве — конфиг, compose с зафиксированными образами, Helm-чарт, конфигурация мониторинга, realm для Keycloak и скрипты для заполнения демо-данными.

**2. Настраиваем конфиг**

```bash
cp config.yaml.example config.yaml
# Открываем config.yaml и меняем пароль администратора и JWT-секрет
```

**3. Запускаем**

```bash
docker compose up -d
```

Через несколько секунд UI доступен на `http://localhost:8081`, логин `admin` / пароль из `config.yaml`. Там же — PostgreSQL на `5437`, MinIO на `9000`, а весь REST API и OCI-реестр (`/v2/…`) живут на том же порту 8081.

Если нужен HA-режим (2 ноды + nginx):

```bash
docker compose -f docker-compose.ha.yml up -d
# UI: http://localhost:8080
```

Дополнительные профили:

```bash
docker compose --profile monitoring up -d      # Prometheus + Grafana
OIDC_ENABLED=true docker compose --profile keycloak up -d   # Keycloak
```

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

Кроме Docker Compose есть нативная установка одним бинарником, deb/rpm/tar.gz для Linux, сборки для macOS и Windows, Helm-чарт и Terraform-провайдер. Для работы из терминала и CI/CD есть отдельная утилита [`nxs`](https://github.com/nexspence/nxs) — умеет публиковать, скачивать и листить компоненты прямо из командной строки или GitHub Actions.

---

## Чего пока нет

Буду честен, список открытый:

- **Group-репозитории есть не для всех форматов** — пока нет для Terraform, APT, Yum, Conan и RubyGems. Proxy и Hosted для них работают.
- **On-demand SBOM и локальная подпись cosign** — в работе, войдут в 3.0.0. Базовые referrers и гейты по severity уже работают.
- **Гайдов 17 из 19 форматов** — для Hugging Face и Yum их пока нет. Пишем.
- **Nexus API — v1, не v3.** Скрипты, которым нужен v3, придётся поправить. Основные сценарии CI (push/pull) работают как раньше.
- **Русский интерфейс есть не везде.** Функционально всё, но часть экранов и текстов на английском.

Это открытые задачи — если что-то из этого нужно, открывайте issues, а если наоборот что-то не нужно — тем более.

Проект живёт на спонсорах: без них не будет ни релизов, ни поддержки. Это не «платная версия» — просто способ не тратить деньги разработчика на инфраструктуру.

---

## Ссылки

- 🌐 Сайт: [nexspence.com](https://nexspence.com)
- 📚 Документация: [nexspence.com/docs](https://nexspence.com/docs/)
- 💻 GitHub: [github.com/nexspence/nexspence](https://github.com/nexspence/nexspence)
- 📦 Релизы: [github.com/nexspence/nexspence/releases](https://github.com/nexspence/nexspence/releases)
- 🔧 CLI `nxs`: [github.com/nexspence/nxs](https://github.com/nexspence/nxs)
- 💬 Telegram-сообщество: [t.me/nexspence_community](https://t.me/nexspence_community)
- ❤️ Спонсоры: [github.com/sponsors/nexspence](https://github.com/sponsors/nexspence)

Если вы так же упёрлись в лимиты Nexus CE — расскажите в комментариях, какой у вас сценарий. Часть фич я делаю именно потому, что её не хватает кому-то конкретному.
