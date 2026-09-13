# Control Center 0.31.1 — corrective package qualification

Текущий опубликованный Public Stable — **0.31.0**, однако его Linux binary asset имеет известный packaging blocker: архив не содержит обязательный `scripts/migrate.sh`, поэтому общий binary install/update path для этого asset не считается самостоятельным подтверждённым путём.

0.31.1 является корректирующим Stable-candidate. Фактический candidate archive уже формируется с обязательным `scripts/migrate.sh`, а package-shape и clean-install acceptance выполняются на собранном package. До завершения поддерживаемых upgrade/recovery проверок, финальной qualification и публикации `v0.31.1` версия не является пользовательски доступным Stable-релизом.

## Что исправляется в 0.31.1

Linux AMD64 archive обязан содержать исполняемый `scripts/migrate.sh` вместе с migrations. Release qualification должна распаковать фактический собранный archive, проверить наличие файла и executable bit и выполнить migration runner именно из package, а не из исходного дерева.

## Обязательная package acceptance

Перед publication 0.31.1 требуется подтвердить:

1. archive checksum и release identity;
2. наличие `scripts/migrate.sh` и migrations внутри фактического archive;
3. clean install PostgreSQL через bundled migration runner;
4. поддерживаемый upgrade с официального предыдущего Stable baseline с использованием неизменяемого официального source/release evidence и bundled migration runner 0.31.1;
5. поддерживаемый переход с уже установленной 0.31.0 на 0.31.1, где этот путь применим;
6. idempotent повтор migrations без destructive downgrade;
7. общие format/vet/unit/race/build/public-boundary gates;
8. сохранение данных, конфигурации и установленного пароля администратора;
9. rollback/forward-recovery и согласованность final release metadata/checksums/provenance/SBOM.

## Ограничение v0.31.0 сохраняется

Tag и release assets `v0.31.0` не переписываются. До публикации квалифицированного corrective patch:

- не выполняйте ручной clean install непосредственно из опубликованного 0.31.0 Linux binary archive;
- не обходите проверку migration runner вручную;
- для exact v0.31.0 используйте только отдельно квалифицированный compatibility path с проверкой release checksum и закреплённого digest migration runner;
- сохраняйте текущую работоспособную установку и recovery point.

## Аутентификация

При корректной чистой установке создаётся локальный пользователь `admin` с первоначальным паролем `admin`. Первый вход требует обязательной смены пароля. Поддерживаемый upgrade сохраняет установленный пользователем пароль и пользовательские данные.
