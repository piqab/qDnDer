# История изменений

[English](CHANGELOG.md) | **Русский**

Здесь документируются все заметные изменения qdnder.
Формат — [Keep a Changelog](https://keepachangelog.com/), версии — semver.

## [Не выпущено]

### Добавлено
- Переопределение токена раннера через окружение `QDNDER_RUNNER_TOKEN`:
  один `.env` compose обслуживает и сервер, и раннер (использует конвейер nkt).
- Release-workflow (`.github/workflows/release.yml`): по тегам `v*` собирает
  бинарники (linux/darwin amd64+arm64, windows amd64) с суммами SHA-256 и
  публикует мультиархитектурные образы ghcr.io (`qdnder`, `qdnder-runner`).
- Конвейеры выкладки через nkt: `deploy/nkt/pipeline-compose.yaml` (один
  compose-стек, токен раннера генерирует хаб, `nkt-example.json` для меню
  «Примеры» хаба) и `deploy/nkt/pipeline-k8s.yaml` (манифесты в кластер).

### Исправлено
- Cookie сессии теперь с `Secure` и `HttpOnly` и при входе, и при выходе
  (алерты CodeQL #1–#3). Удалённый доступ к UI требует HTTPS; localhost
  по-прежнему работает по обычному HTTP.

## [0.1.0] - 2026-11-24

### Добавлено
- Начальный репозиторий: лицензия MIT, README на двух языках.
- Ядро сервера: хранилище SQLite/PostgreSQL, пользователи и роли
  (admin/operator/viewer), API-токены, проекты с секретами registry/git/
  вебхука, очередь сборок, REST API.
- Раннер с rootless BuildKit (buildctl), живые логи через SSE, отмена
  сборок в очереди и идущих, хартбиты раннеров.
- Пайплайны `qdnder.yaml` (плоский стиль nkt): переопределение dockerfile/
  context/image/tags/build_args, шаги до/после сборки; шаблоны тегов
  {tag}, {branch}, {sha}, {sha:N}, {date}.
- Вебхуки: Gitea/Forgejo/GitHub (HMAC-SHA256) и GitLab (токен), push и теги.
- MCP-сервер на /mcp (JSON-RPC 2.0): qdnder_builds, qdnder_build, qdnder_logs,
  qdnder_cancel с проверкой ролей.
- Встроенные веб-морда и админка (русский/английский): сборки с живыми
  логами, проекты, пользователи, токены, раннеры, смена токена раннера.
- Docker Compose (server + rootless buildkitd + runner, опциональный профиль
  PostgreSQL) и манифесты Kubernetes (server + PVC или DSN PostgreSQL,
  rootless BuildKit StatefulSet, масштабируемый Deployment раннера).
- Плагин хаба nkt как загружаемый вручную архив (протокол v1, ru/en,
  HMAC-подпись вызовов, тесты на подделках): таблица сборок, лог, форма
  запуска, отмена с подтверждением, сводка с находками, команда сценария
  `qdnder …`, инструменты MCP; `make plugin-archive` собирает tar.gz + zip.
- Связка с nkt без секретов: semver-теги образов для `registry_poll` хаба.
