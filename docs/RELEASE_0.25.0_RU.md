# Control Center 0.25.0 — ограниченное чтение Audit

## Что нового

Control Center 0.25.0 добавляет permission-gated read-only API для безопасного просмотра событий Audit без ослабления append-only свойств журнала.

- `GET /api/v1/audit/events` доступен только аутентифицированной identity с глобальным разрешением `audit.events.read` и после обязательной смены первоначального пароля.
- Чтение ограничено: используются bounded pagination, непрозрачный cursor и точные фильтры `action`, `outcome`, `actor_id`, `subject_id`; неограниченный экспорт и произвольные запросы не поддерживаются.
- Ответ выдаётся от новых событий к старым, помечается `Cache-Control: no-store` и повторно применяет безопасное представление structured details.
- PostgreSQL reader проверяет hash каждого загружаемого события перед выдачей и работает fail-closed при ошибках decode/integrity.
- Успешное привилегированное чтение само создаёт Audit evidence; если evidence записать невозможно, события клиенту не возвращаются.
- Реализация одинаково покрыта in-memory и PostgreSQL reader contract, HTTP contract и интеграционными тестами.

## Qualification

Кандидат должен пройти полный существующий deterministic CI/qualification-контур: unit/contracts, format/vet, build, public-safety, race/restart и PostgreSQL 15–18 clean-install/supported-upgrade/adapters. Gemini используется только как дополнительный exact-SHA reviewer при фактической доступности провайдера.

Публикация в `main`, tag и GitHub Release выполняется отдельным CC Release Engine B после успешного qualification.
