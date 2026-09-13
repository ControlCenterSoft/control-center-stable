# Control Center 0.31.0 Stable

Статус: **Public Stable**.

0.31.0 вводит основной безопасный operational workflow вокруг Changes и Jobs.

## Что нового

- точная immutable revision Change, semantic diff и blast radius;
- execution preflight и maintenance-window evidence;
- approval evidence, привязанное к точной revision/hash;
- durable Job lifecycle и timeline;
- reconnect с version/ETag identity;
- version-bound cancel и bounded manual retry с повторной revalidation;
- terminal result и post-condition evidence;
- recovery-path evidence и защита от false Success;
- read-only Changes / Jobs operator view с fail-closed обработкой stale/incomplete evidence.

## Информационная безопасность

- универсальный shell/command execution API не добавляется;
- stale revision, approval или Job version не принимается как current;
- неподтверждённый terminal result не становится Success;
- secrets, credentials, raw Job input/output и lease material не входят в operator evidence;
- браузерный auth flow сохраняет безопасный redirect/login boundary, а API продолжает возвращать машинно-читаемый 401 без HTML-редиректа;
- публичные source/privacy boundaries и неизменяемость ранее опубликованных migrations проверяются fail-closed.

## Upgrade, recovery и совместимость

Release-candidate qualification подтверждала PostgreSQL 15–18 migration/adapter paths, static/unit/contracts/build, race/restart safety, upgrade/recovery semantics и воспроизводимую Linux AMD64 упаковку. Поддерживаемый upgrade design сохраняет данные, настройки и установленный пароль администратора.

### Известная проблема опубликованного Linux asset

После публикации `v0.31.0` выявлено расхождение формы фактического Linux runtime package с install/update contract: `control-center-0.31.0-linux-amd64.tar.gz` не содержит обязательный `scripts/migrate.sh`. Публичный installer и canonical updater ожидают этот файл внутри runtime archive и поэтому останавливаются fail-closed.

Контрольная сумма опубликованного файла корректна, но она подтверждает целостность именно этого payload и не доказывает работоспособность install/upgrade path. Existing tag/release/assets `v0.31.0` не переписываются.

До corrective patch или отдельной квалифицированной exact-tag fallback procedure не считайте one-command clean install/update через Linux binary archive 0.31.0 поддерживаемым путём и не обходите migration-runner check вручную. Актуальная безопасная инструкция поддерживается в [INSTALL.md](INSTALL.md).

## Лицензирование компонентов

Выпуск содержит machine-readable third-party manifest, лицензии зависимостей и `THIRD_PARTY_NOTICES.md`. Коммерческие договорные условия ведутся отдельно от технической квалификации продукта и не подменяются техническим release evidence.
