# Control Center 0.31.1 — corrective Release Candidate

## Статус

0.31.1 подготовлен как исправляющий кандидат для линии 0.31.x. До завершения обязательной qualification и публикации отдельного release/tag версия **не является Public Stable**. Текущим опубликованным Public Stable остаётся 0.31.0.

## Назначение выпуска

0.31.1 устраняет release-package regression опубликованного Linux asset 0.31.0. Функциональное расширение Control Center в этот patch не входит.

Опубликованный `control-center-0.31.0-linux-amd64.tar.gz` не содержит обязательный `scripts/migrate.sh`. Existing `v0.31.0` tag/release/assets сохраняются неизменяемыми.

## Исправления release contract

- build script проверяет состав фактически созданного tar.gz до формирования checksum;
- отсутствие исполняемого `bin/control-center` или `scripts/migrate.sh`, systemd unit либо корректного `VERSION` блокирует package acceptance;
- artifact contract и JSON Schema синхронизированы и требуют одинаковый полный набор из 9 release artifacts;
- candidate identity — 0.31.1;
- qualification содержит отдельные upgrade gates с 0.30.0 и 0.31.0;
- путь из 0.31.0 учитывает известное отсутствие migration runner в опубликованном binary asset и допускает только runner из того же immutable tag после проверки закреплённой SHA-256.

## Условия перевода в Public Stable

Перед публикацией должны быть подтверждены:

- reproducible Linux AMD64 packaging;
- archive-membership/executable gate;
- clean install;
- `0.30.0 → 0.31.1`;
- `0.31.0 → 0.31.1`;
- PostgreSQL migration/restart и idempotency;
- сохранение данных, конфигурации и установленного пароля администратора;
- rollback/forward-recovery;
- security/privacy/public-source checks;
- checksums, release manifest, provenance, SBOM и third-party notices;
- фактический публичный installer/update flow.

Ни один gate не считается пройденным только на основании ожидаемого поведения или evidence от предыдущего candidate SHA.

## Аутентификация

Для clean install сохраняется контракт: локальный `admin` / `admin` только для первого входа, после чего обязательна смена пароля. При upgrade пользовательский пароль администратора сохраняется и не сбрасывается.

## Recovery

Перед обновлением требуется валидная резервная копия и проверяемая точка восстановления. Успешность upgrade подтверждается только после migrations, restart и health/version verification. Неуспешный upgrade должен переходить в предусмотренный rollback/forward-recovery path без ручной подмены release payload.
