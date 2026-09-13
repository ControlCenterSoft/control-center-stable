# Control Center 0.31.1 — Public Stable

## Статус

Control Center 0.31.1 опубликован 13.09.2026 как текущий **Public Stable** release линии 0.31.x. Официальная release identity — `v0.31.1`.

0.31.1 является корректирующим patch-релизом поверх 0.31.0 и не расширяет функциональный scope Control Center. Исторический `v0.31.0` и его опубликованные assets остаются неизменяемыми.

## Назначение выпуска

0.31.1 устраняет release-package regression Linux asset 0.31.0: в опубликованном архиве 0.31.0 отсутствовал обязательный исполняемый `scripts/migrate.sh`.

В 0.31.1 полный package/install contract восстановлен: Linux archive содержит обязательные runtime-компоненты и migration runner, а выпуск прошёл отдельную проверку установки и поддерживаемых обновлений.

## Подтверждённые пути установки и обновления

Для 0.31.1 подтверждены:

- clean install 0.31.1;
- `0.30.0 → 0.31.1`;
- `0.31.0 → 0.31.1`;
- PostgreSQL migration/restart и idempotency;
- сохранение пользовательских данных, конфигурации и установленного пароля администратора;
- rollback/forward-recovery;
- post-publication verification опубликованных release artifacts.

Для исторического 0.31.0 сохраняется только ограниченный compatibility path, описанный в актуальных инструкциях установки/обновления. Он не является общим исключением из package integrity policy и не применяется к другим версиям.

## Release artifacts

Официальный `v0.31.1` включает Linux AMD64 archive, отдельную SHA-256 checksum, source archive, release/qualification metadata, provenance, CycloneDX SBOM и third-party notices. Перед установкой или обновлением необходимо проверять опубликованную checksum и использовать только официальные release artifacts.

## Аутентификация

Для clean install сохраняется контракт: локальный пользователь `admin` с первоначальным паролем `admin` используется только для первого входа. При первом входе смена пароля обязательна; до неё обычная работа с системой не должна быть доступна.

При обновлении пользовательский пароль администратора сохраняется и не сбрасывается на `admin`.

## Recovery

Перед обновлением требуется валидная резервная копия и проверяемая точка восстановления. Успешность upgrade подтверждается после migrations, restart и health/version verification. При неуспешном обновлении должен использоваться предусмотренный rollback/forward-recovery path без ручной подмены release payload.
