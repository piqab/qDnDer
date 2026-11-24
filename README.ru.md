# qdnder

[English](README.md) | **Русский**

Независимый CI-сервис для сборки Docker-образов — как GitHub Actions или
Forgejo runner, но полностью свой: свой сервер, свои раннеры, своя веб-морда.
Без привязки к какому-либо git-хостингу.

Один бинарник Go, два режима:

```
qdnder server   сервер: REST API, веб-морда, админка, MCP-сервер
qdnder runner   воркер: clone → сборка BuildKit → push во внешний registry
```

- **Два способа сборки**: простая сборка (репозиторий + Dockerfile → образ)
  или плоский пайплайн `qdnder.yaml` в репозитории (шаги до/после, шаблоны
  тегов, build-args) — в том же стиле, что `pipeline.yaml` nkt.
- **Триггеры**: вебхуки (Gitea / Forgejo / GitHub / GitLab с проверкой HMAC),
  REST API с токенами по правам и MCP-сервер для ИИ-агентов.
- **Rootless BuildKit** — без privileged-контейнеров, работает и в Docker
  Compose, и в Kubernetes.
- **Дружит с nkt**: пуш semver-тегов (`v1.2.3`) — и хаб
  [nkt](https://github.com/piqab/nkt) сам выкладывает их через
  `registry_poll`, секретов хаба в CI нет. В комплекте — плагин хаба nkt,
  загружаемый вручную архивом.
- **Хранение**: SQLite по умолчанию (один хост), PostgreSQL по DSN (VPS /
  кластер).
- **Авторизация**: свои пользователи с ролями (admin / operator / viewer) +
  API-токены. Без OIDC.
- **Веб-морда и админка** встроены в бинарник, русский и английский языки.

## Быстрый старт (Docker Compose)

```bash
cd deploy
docker compose up -d --build
docker compose logs server   # пароль администратора при первом запуске
```

Откройте http://localhost:8090 — войдите, создайте проект (адрес репозитория,
имя образа, доступ к registry), нажмите **«Собрать»**. Раннеры подхватывают
задачи сами.

> Cookie сессии — `Secure`: браузеры принимают их на `localhost` /
> `127.0.0.1` и по обычному HTTP, но для удалённого доступа нужен HTTPS
> (поставьте TLS reverse-proxy — см. [deploy/README.md](deploy/README.md)).

## Быстрый старт (бинарник)

Скачайте готовый бинарник из [GitHub Releases](https://github.com/piqab/qDnDer/releases)
(linux/darwin amd64/arm64, windows amd64; контрольные суммы SHA-256 в комплекте)
или соберите:

```bash
make build
QDNDER_ADDR=127.0.0.1:8090 ./qdnder server &   # пароль админа при первом запуске
QDNDER_SERVER=http://127.0.0.1:8090 \
QDNDER_RUNNER_TOKEN=<токен из админки> \
BUILDKIT_HOST=tcp://127.0.0.1:1234 ./qdnder runner
```

Раннеру нужны `git` и `buildctl` в PATH и доступный BuildKit-демон
(`buildkitd --addr tcp://127.0.0.1:1234` или служба `buildkitd` из compose).

## qdnder.yaml

Необязательный файл в корне репозитория; переопределяет настройки проекта:

```yaml
dockerfile: Dockerfile
context: .
image: registry.example.com/team/app
tags: [latest, "{tag}", "{sha:12}"]
build_args:
  VERSION: "{tag}"
steps:
  - name: tests
    run: ./run-tests.sh
    when: before        # before | after — до или после сборки образа
```

Шаблоны тегов: `{tag}`, `{branch}`, `{sha}`, `{sha:N}`, `{date}`.
Без файла используются настройки проекта как есть.

## Разворачивание

- **Docker Compose**: [`deploy/docker-compose.yml`](deploy/docker-compose.yml)
  — server + rootless buildkitd + runner; опциональный профиль PostgreSQL.
- **Kubernetes**: [`deploy/k8s/`](deploy/k8s/) — Deployment сервера + PVC
  (SQLite) или DSN PostgreSQL, rootless BuildKit StatefulSet, Deployment
  раннера.
- **Через хаб nkt**: [`deploy/nkt/pipeline-compose.yaml`](deploy/nkt/pipeline-compose.yaml)
  — один compose-стек (токен раннера генерирует хаб);
  [`deploy/nkt/pipeline-k8s.yaml`](deploy/nkt/pipeline-k8s.yaml) — манифесты
  в кластер; [`deploy/nkt/deploy-host.nkt`](deploy/nkt/deploy-host.nkt) —
  сценарий на хост из готовых образов. Шаги — в [deploy/README.md](deploy/README.md).
- **Плагин хаба nkt**: [`plugin/qdnder/`](plugin/qdnder/) — соберите
  `make plugin-archive`, затем хаб → «О системе» → «Плагины» → «Каталог» →
  **«Загрузить свой плагин»** (`qdnder-plugin.tar.gz` или `.zip`).

## API и MCP

REST API под `/api` с `Authorization: Bearer <токен>`; MCP на `/mcp`
(JSON-RPC 2.0): инструменты `qdnder_builds`, `qdnder_build`, `qdnder_logs`,
`qdnder_cancel`. Токен раннера и адреса вебхуков — в админке
(**«Настройки» → API**).

## Разработка

```bash
make build   # go build
make test    # go test ./... + тесты плагина
```

## Лицензия

MIT — [LICENSE](LICENSE).
