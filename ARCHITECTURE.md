# Архитектура Control Center

Статус: **публичное нормативное описание архитектурных границ продукта**.

Control Center — самостоятельная платформа централизованного управления серверной и пользовательской ИТ‑инфраструктурой. Архитектура строится вокруг типизированных, проверяемых и аудируемых изменений без произвольного удалённого shell как публичной модели управления.

## 1. Базовый путь изменения состояния

Канонический путь state-changing операции:

`Запрос → Валидация → Авторизация → План → Change → Job → типизированное действие → post-condition verification → Actual State → Audit → recovery/rollback при необходимости`.

Обязательные инварианты:

- RBAC работает deny-by-default и проверяется на сервере;
- Desired State отделён от Actual State;
- длительные изменения выполняются через Change/Job;
- действия типизированы и ограничены allowlisted capabilities;
- stale state и конфликтующие поколения отклоняются;
- успешный запуск команды не равен успешному результату;
- опасная операция имеет preflight, понятный риск и проверяемый recovery/rollback path;
- секреты не помещаются в обычные API/evidence contracts в открытом виде.

## 2. Core

Core содержит общие платформенные подсистемы:

- Identity и RBAC;
- Desired/Actual State;
- Changes/Jobs и execution boundaries;
- Node/Role/Lifecycle;
- Managed Network;
- Monitoring/Health;
- Audit и integrity evidence;
- Backup/Recovery contracts;
- Capacity Planner foundation;
- общие API/security boundaries.

## 3. Single-node и multi-node/HA

Single-node является полноценным режимом эксплуатации и не рассматривается как деградированный вариант.

Multi-node/HA добавляет роли и распределение сервисов только там, где это реально поддерживается. Для stateful ролей должны быть определены quorum/fencing/split-brain prevention, replication semantics, switchover/failover/failback и recovery. HA нельзя объявлять поддержанным только по наличию нескольких узлов или архитектурного контракта: нужны практические failure/recovery tests.

Lifecycle узла включает enrollment, active, maintenance, drain, replacement и decommission. Перед destructive operation обязательны dependency/redundancy/data-safety checks.

## 4. Данные и PostgreSQL

PostgreSQL используется для транзакционного состояния Control Center там, где это предусмотрено соответствующей capability. Schema evolution выполняется forward migrations; опубликованные migrations не переписываются задним числом.

Stateful workloads требуют provider-specific backup/migration/recovery semantics. Backup без проверенного restore не является доказанной готовностью восстановления. Для критичных данных должны существовать recovery points, integrity metadata и restore drills.

## 5. Audit

Audit фиксирует административные и security-relevant события и является частью доверенной границы продукта. Read access permission-gated и bounded. Integrity verification должна обнаруживать разрыв или повреждение append-only chain и работать fail-closed при невозможности подтвердить целостность.

Успешная privileged verification сама оставляет Audit evidence до возврата положительного результата.

## 6. Managed Network

Network Management является first-class подсистемой и проектируется для multi-NIC узлов, WAN/LAN, zones, VLAN/bonding где поддерживается, routing, DNS/NTP и firewall policy.

Изменения сети выполняются staged: plan → validation → apply → connectivity/post-condition verification. При потере управляемости должен существовать automatic rollback или заранее подготовленный recovery path.

WAN+LAN не включает routing автоматически. NAT, port-forwarding и external publication разрешаются только отдельным явным действием.

## 7. Capacity Planner

Capacity Planner использует доступные hardware/resource характеристики, telemetry, storage/DB/network constraints, workload profiles и growth trends. Он должен предоставлять safe capacity, прогноз исчерпания резерва, bottlenecks, what-if моделирование и рекомендации по размещению/переносу ролей и сервисов.

Рекомендации являются advisory-only, пока отдельная policy-driven capability не разрешит автоматическое действие для конкретного workload.

## 8. Market

Market-модуль не получает обходной execution path. Для каждого модуля определяются:

- identity и version;
- compatibility/dependencies/conflicts;
- permissions/capabilities;
- network/storage requirements;
- capacity profile;
- lifecycle Install → Configure → Health → Update → Migrate/Drain → Backup → Restore → Remove;
- backup/recovery semantics;
- legal/compliance metadata и license evidence.

Directory Services, DNS/DHCP, PXE, Software Automation, inventory, File Services, Monitoring и Backup используют общие platform boundaries.

## 9. Аутентификация

После чистой установки создаётся локальный пользователь `admin` с первоначальным паролем `admin`. Первый вход требует обязательной смены; до успешной смены обычная работа запрещена. Upgrade сохраняет установленный пользователем пароль и не сбрасывает его к первоначальному значению.

Локальная аварийная административная identity не должна зависеть от доступности внешнего directory provider. Интеграция с directory services добавляется как отдельный provider с явными permissions и health semantics.

## 10. Безопасность и публичная граница

Публичные API не предоставляют произвольный shell/exec. Привилегированные операции используют типизированные actions, явную authorization и Audit evidence. Network/listener публикация требует TLS и explicit policy.

Публичные документы, examples и артефакты продукта не должны содержать реальные credentials, private keys, внутренние адреса, operator-specific topology/identifiers, внутреннюю инфраструктуру или методологию разработки, runner-инфраструктуру, внутренние repository/branch details и названия внутренних AI/reviewer-процессов.

## 11. Definition of Done

Функция считается готовой только когда её фактическая реализация соответствует contracts, RBAC, stale/idempotency, failure/recovery, security tests, install/upgrade semantics, backup/restore требованиям и пользовательской документации. Unknown, stale или degraded состояние нельзя выдавать за Healthy или Success.
