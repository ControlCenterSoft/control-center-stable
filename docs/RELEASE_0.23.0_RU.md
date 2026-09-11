# Control Center 0.23.0 — RBAC self-introspection

## Что нового

Control Center 0.23.0 добавляет безопасный read-only API для просмотра текущим аутентифицированным пользователем собственных назначений RBAC.

- `GET /api/v1/identity/self/access` возвращает только права текущей identity; выбрать другого пользователя через запрос невозможно.
- Ответ содержит детерминированно упорядоченные роли, scope и permissions, включая явный wildcard `*` без искусственного разворачивания.
- Пользователь без bindings получает пустой список grants без неявного разрешения доступа.
- API недоступен до обязательной смены первоначального пароля после чистой установки.
- Чтение security-sensitive данных фиксируется в Audit; при недоступности обязательного Audit endpoint работает fail-closed.
- Ответ помечается `Cache-Control: no-store` и не содержит пароли, session tokens, cookie или token digests.
- Реализация поддерживает одинаковую модель introspection для in-memory authorizer и PostgreSQL-backed RBAC.

## Qualification

Кандидат должен пройти полный существующий deterministic CI/qualification-контур, включая unit/integration, format/vet, build, public-safety и PostgreSQL clean-install/supported-upgrade/adapter matrix. Gemini используется только как дополнительный exact-SHA reviewer при доступности провайдера.

Публикация в `main`, создание tag и GitHub Release выполняются отдельным Release Engine после успешного qualification.
