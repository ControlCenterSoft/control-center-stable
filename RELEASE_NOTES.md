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

Независимая Stable qualification подтверждает PostgreSQL 15–18 migration/adapter paths, static/unit/contracts/build, race/restart safety, upgrade/recovery semantics и воспроизводимую Linux AMD64 упаковку. Поддерживаемое обновление с 0.30.0 сохраняет данные, настройки и установленный пароль администратора.

## Лицензирование компонентов

Выпуск содержит machine-readable third-party manifest, лицензии зависимостей и `THIRD_PARTY_NOTICES.md`. Коммерческие договорные условия ведутся отдельно от технической квалификации продукта и не подменяются техническим release evidence.
