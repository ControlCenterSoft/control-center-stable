# Архитектура Control Center

Control Center — самостоятельный инфраструктурный продукт для администраторов.

## Основные принципы

Система разделяет Desired State и Actual State. Изменения проходят управляемый путь: Identity/RBAC → план изменения → подтверждение → Job → выполнение → повторное чтение фактического состояния → проверка → Audit/evidence → rollback или recovery при необходимости. Административные полномочия deny-by-default; произвольный shell/exec не является универсальным пользовательским API.

## Развёртывание и роли

Single-node является полноценным поддерживаемым режимом. Multi-node/HA применяется только там, где определены quorum, failure/recovery и upgrade-процедуры. Один физический сервер может совмещать роли при достаточной ёмкости и соблюдении требований отказоустойчивости.

## Сеть

Поддерживается multi-NIC модель с явными ролями интерфейсов и зон. WAN+LAN сам по себе не включает routing, NAT или port-forwarding. Сетевые изменения должны выполняться staged: план → preflight → подтверждение → применение → connectivity validation → commit либо автоматический rollback.

## Данные, backup и recovery

Транзакционное состояние хранится в PostgreSQL. Backup считается доказанным только при наличии проверяемого restore. Для stateful-сервисов обязательны специализированные backup/recovery процедуры; HA не считается поддержанным без failure/recovery испытаний.

## Lifecycle и обновления

Maintenance, drain, replacement и decommission учитывают зависимости, capacity reserve, redundancy/quorum и сохранность данных. Обновление выполняется с preflight, совместимостью, backup/recovery prerequisites, health-check после каждого шага и rollback/forward-recovery. В multi-node профилях применяется безопасный rolling-порядок.

## Аутентификация и аудит

Чистая установка создаёт `admin` / `admin`; первый вход требует обязательной смены пароля. Обновление не сбрасывает установленный пользователем пароль. Опасная операция показывает цель, изменение, риск, зависимости, preflight, критерий успеха и recovery path; привилегированные действия фиксируются в Audit без раскрытия секретов.

## Capacity Planner и Market

Capacity Planner использует CPU/RAM/storage/network/DB/workload-данные для safe capacity, bottleneck, прогнозов и placement/what-if рекомендаций. Market-модули имеют версионируемые manifests, dependencies/conflicts, permissions, network/storage/capacity requirements, lifecycle и проверяемую license/compliance metadata.
