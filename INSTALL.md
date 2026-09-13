# Control Center 0.31.1 — corrective package qualification

Текущий опубликованный Public Stable — **0.31.0**, однако его Linux binary asset имеет известный packaging blocker: архив не содержит обязательный `scripts/migrate.sh`, поэтому one-command install/update корректно останавливается fail-closed.

Ветка 0.31.1 является корректирующим Stable-candidate. До завершения exact-head qualification и публикации `v0.31.1` она не является пользовательски доступным Stable-релизом.

## Что исправляется в 0.31.1

Linux AMD64 archive обязан содержать исполняемый `scripts/migrate.sh` вместе с migrations. Qualification должна распаковать фактический собранный archive, проверить member/executable bit и выполнить migration runner именно из package, а не из checkout tree.

## Обязательная package acceptance

Перед publication 0.31.1 требуется подтвердить:

1. archive checksum и release identity;
2. наличие исполняемого `scripts/migrate.sh`, binary, systemd unit и корректного `VERSION` внутри фактического archive;
3. clean install PostgreSQL через bundled migration runner;
4. supported upgrade `0.30.0 → 0.31.1`;
5. supported update/recovery path `0.31.0 → 0.31.1`, учитывающий известный package-shape defect 0.31.0 и использующий только проверенный immutable-tag migration runner;
6. idempotent повтор migration без destructive downgrade;
7. общие format/vet/unit/race/build/public-boundary gates;
8. согласованность final release metadata/checksums/provenance/SBOM/third-party notices после promotion.

## Ограничение v0.31.0 сохраняется

Tag и release assets `v0.31.0` не переписываются. До публикации квалифицированного corrective patch:

- не начинайте clean install через опубликованный 0.31.0 Linux binary archive;
- не используйте этот archive как непроверенный штатный upgrade source;
- не подменяйте migration runner произвольным файлом;
- сохраняйте текущую работоспособную установку и recovery point.

Если для 0.31.0 используется официальный compatibility path, migration runner должен быть получен только из того же immutable tag `v0.31.0` и принят только после проверки закреплённой SHA-256.

## Аутентификация

При корректной чистой установке создаётся локальный пользователь `admin` с первоначальным паролем `admin`. Первый вход требует обязательной смены пароля. Supported upgrade сохраняет установленный пользователем пароль, пользовательские данные и конфигурацию.

## Recovery

Перед обновлением требуется рабочая резервная копия и проверяемая точка восстановления. Успех upgrade подтверждается только после migrations, restart и health/version verification. При неуспешной проверке применяется предусмотренный rollback/forward-recovery path, а не ручная подмена release payload.
