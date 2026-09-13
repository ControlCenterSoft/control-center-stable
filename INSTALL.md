# Control Center 0.31.1 — corrective package qualification

Текущий опубликованный Public Stable — **0.31.0**, однако его Linux binary asset имеет известный packaging blocker: архив не содержит обязательный `scripts/migrate.sh`, поэтому one-command install/update корректно останавливается fail-closed.

Ветка 0.31.1 является корректирующим Stable-candidate. До завершения exact-head qualification и публикации `v0.31.1` она не является пользовательски доступным Stable-релизом.

## Что исправляется в 0.31.1

Linux AMD64 archive обязан содержать исполняемый `scripts/migrate.sh` вместе с migrations. Runner qualification должна распаковать фактический собранный archive, проверить member/executable bit и выполнить migration runner именно из package, а не из checkout tree.

## Обязательная package acceptance

Перед publication 0.31.1 требуется подтвердить:

1. archive checksum и release identity;
2. наличие `scripts/migrate.sh` и migrations внутри фактического archive;
3. clean install PostgreSQL через bundled migration runner;
4. supported upgrade с опубликованного Stable 0.30.0 через migration runner обоих package;
5. idempotent повтор migration без destructive downgrade;
6. общие format/vet/unit/race/build/public-boundary gates;
7. согласованность final release metadata/checksums/provenance после promotion.

## Ограничение v0.31.0 сохраняется

Tag и release assets `v0.31.0` не переписываются. До публикации квалифицированного corrective patch:

- не начинайте clean install через опубликованный 0.31.0 Linux binary archive;
- не используйте этот archive для штатного upgrade;
- не обходите migration-runner check вручную;
- сохраняйте текущую работоспособную установку и recovery point.

## Аутентификация

При корректной чистой установке создаётся локальный пользователь `admin` с первоначальным паролем `admin`. Первый вход требует обязательной смены пароля. Supported upgrade сохраняет установленный пользователем пароль и пользовательские данные.
