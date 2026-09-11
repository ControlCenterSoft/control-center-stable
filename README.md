# Control Center

Control Center — централизованная платформа управления инфраструктурой с типизированной, проверяемой и аудируемой моделью исполнения.

Текущий кодовый baseline: **0.6.0**. Номер версии изменяется только отдельным релизным процессом после реализации и тестирования.

## Источник истины разработки

Нормативным источником истины для текущей разработки является ветка `main` этого репозитория: `ControlCenterSoft/control-center-development`.

Перед продолжением разработки необходимо читать:

1. [`ARCHITECTURE.md`](ARCHITECTURE.md) — целевая архитектура и обязательные инварианты;
2. [`ROADMAP.md`](ROADMAP.md) — правильная последовательность внедрения и первый незакрытый архитектурный этап;
3. [`docs/REQUIREMENTS_RU.md`](docs/REQUIREMENTS_RU.md) — каталог уже принятых требований.

Правило: команда «продолжай разработку» должна сначала сверять актуальный `main`, активные PR и первый незакрытый этап `ROADMAP.md`, а затем реализовывать следующий совместимый Task Packet.

`ControlCenterSoft/control-center-stable` — стабильный релизный канал. `ControlCenterSoft/control-center` — публичный сайт/витрина. Эти репозитории не являются архитектурным source of truth продукта.

Google Drive содержит подробную продуктовую/эксплуатационную документацию и должен быть синхронизирован с этими нормативными файлами. Выявленное противоречие между реализацией и документацией должно быть устранено до развития конфликтующего контракта.

## Возможности текущего baseline

- HTTP/JSON API и health/readiness endpoints;
- локальная identity/session модель;
- deny-by-default RBAC;
- append-oriented audit;
- PostgreSQL-backed durable state;
- immutable configuration revisions;
- policy/risk/approval-aware Changes;
- durable Jobs с leases, retries и idempotency;
- allowlisted typed Worker actions;
- resource state/health;
- Agent enrollment/heartbeat foundations;
- Inventory/Market/PXE/Automation/Domain/Integration foundations;
- non-root runtime.

Целевая распределённая ролевая, кластерная, Capacity, Lifecycle/Recovery, Network/Edge и Enterprise Market архитектура описана в нормативных документах выше и внедряется поэтапно, а не одним несовместимым скачком.

## Модель разработки

Разработка ведётся параллельно, но общие контракты Identity/RBAC/State/Jobs/Agent/Market/Network/Recovery не должны иметь независимых несовместимых реализаций в разных ветках.

Pull request должен оставлять `main` зелёным и проходить предусмотренные форматирование, vet/race, unit/integration/security/failure/build gates.

В репозитории запрещены credentials, приватная топология инфраструктуры, production data, приватные deployment endpoints и секреты.

## Локальная проверка

```bash
make ci
```

## Локальная сборка

```bash
make build
./bin/control-center
```

## Первый вход

На пустой установке Control Center создаёт локального пользователя `admin` с одноразовым начальным паролем `admin`. Первая сессия позволяет только проверить состояние сессии, сменить пароль или выйти. До смены пароля обычная работа запрещена. Обновление установленной системы никогда не заменяет существующий пароль пользователя и не восстанавливает начальный credential.
