# Control Center 0.31.0 — Public Stable

0.31.0 добавляет основной operational workflow Changes / Jobs с точной revision, semantic diff, blast radius, preflight, approval evidence, durable Job lifecycle, version-bound reconnect/cancel/retry, result verification и recovery evidence.

Безопасность построена fail-closed: stale/mismatched evidence не принимается как current, принятие запроса не равно подтверждённому результату, а terminal `succeeded` без корректного post-condition evidence не становится подтверждённым Success.

Поддерживаемое обновление с 0.30.0 квалифицировано с сохранением данных, настроек и установленного пароля администратора. PostgreSQL 15–18 migration/adapter paths, race/restart, rollback/forward-recovery и воспроизводимая упаковка проверены для точной promoted candidate identity.

Public Stable содержит third-party notices/license evidence. Неподтверждённые коммерческие или юридические гарантии не заявляются техническим релизом.
