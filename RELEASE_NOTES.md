# Control Center 0.31.1 — corrective release candidate

Статус: **Release Candidate / не Public Stable до завершения qualification и публикации**.

0.31.1 — исправляющий patch для release/package path Control Center 0.31.x. Функциональное расширение продукта относительно 0.31.0 не является целью этого выпуска.

## Что исправляется

- Linux runtime archive обязан содержать исполняемый `scripts/migrate.sh`;
- packaging завершается fail-closed, если фактически созданный tar.gz не содержит обязательный binary, migration runner, systemd unit или корректный `VERSION`;
- verification contract проверяет состав распакованного release archive, а не только его SHA-256;
- machine-readable artifact contract и JSON Schema синхронизированы: обязательны binary archive, checksum, source archive, SBOM, third-party notices, provenance, qualification evidence, release manifest и `SHA256SUMS`;
- qualification 0.31.1 отдельно требует upgrade path с 0.30.x и с текущей линии 0.31.0.

## Причина corrective patch

Опубликованный `v0.31.0` остаётся неизменяемым, но его Linux runtime asset `control-center-0.31.0-linux-amd64.tar.gz` не содержит обязательный `scripts/migrate.sh`. Корректная SHA-256 подтверждает целостность опубликованного файла, но не корректность его package shape. Перезапись существующего tag/release/assets не допускается.

## Обязательная qualification перед Public Stable

0.31.1 может стать Public Stable только после подтверждения всех release-blocking gates:

1. reproducible packaging и archive-membership contract;
2. clean install;
3. поддерживаемый `0.30.x → 0.31.1` upgrade;
4. `0.31.0 → 0.31.1` recovery/update path;
5. PostgreSQL migration/restart;
6. сохранение данных, конфигурации и установленного пользователем пароля администратора;
7. rollback/forward-recovery;
8. security/privacy/public-source checks;
9. checksums, manifest, provenance, SBOM и third-party notices;
10. фактическая проверка публичного installer/update flow.

До прохождения этих проверок текущим официальным Public Stable остаётся 0.31.0 с опубликованным known issue и безопасными ограничениями из `INSTALL.md`.

## Аутентификация

При корректной чистой установке создаётся локальный пользователь `admin` с первоначальным паролем `admin`; при первом входе смена пароля обязательна. Обновление существующей установки не должно сбрасывать установленный пользователем пароль.

## Лицензирование компонентов

Release package должен включать machine-readable SBOM и `THIRD_PARTY_NOTICES.md`. Коммерческие договорные условия ведутся отдельно от технической qualification и не подменяют security, upgrade, recovery и release-identity gates.
