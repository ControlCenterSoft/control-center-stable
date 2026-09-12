# Архитектура Control Center

Control Center — самостоятельный инфраструктурный продукт для администраторов.

Система разделяет Desired State и Actual State. Изменения проходят управляемый путь: Identity/RBAC → Change → exact approval → durable Job → typed execution → Actual State/post-condition verification → Audit/evidence → rollback/recovery. Произвольный shell/exec не является универсальным пользовательским API.

Changes / Jobs workflow использует immutable revision identity, semantic diff, blast radius, preflight, approval evidence, version-bound cancel/retry, подтверждение результата и recovery evidence. Unknown, Stale, Degraded и неполное evidence не могут отображаться как Healthy/Success.

Single-node является полноценным поддерживаемым режимом. Multi-node/HA заявляется только для профилей, где определены quorum, failure/recovery и upgrade-процедуры. WAN+LAN не включает routing/NAT автоматически; network mutation должна иметь staged verification и rollback.

Транзакционное состояние хранится в PostgreSQL. Backup считается доказанным только при проверяемом restore. Опубликованные migrations immutable byte-for-byte; изменение схемы выполняется только новой migration.

Чистая установка создаёт `admin` / `admin` и требует смены пароля при первом входе. Обновление не сбрасывает установленный пользователем пароль. Привилегированные действия проходят RBAC и Audit без раскрытия секретов.
