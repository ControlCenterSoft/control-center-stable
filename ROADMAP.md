# Control Center — продуктовая дорожная карта

Статус: **CURRENT**. Текущий официальный Public Stable — **0.27.0**.

## Опубликованная основа

Control Center развивается как самостоятельный инфраструктурный продукт для администраторов. Опубликованная Stable-линия включает Identity/RBAC/Audit, Changes/Jobs, lifecycle/recovery contracts, Site/Network foundation, advisory Capacity Intelligence, Session Security Policy, защищённый локальный вход, bounded Audit access и recovery evidence freshness 0.27.0.

## Архитектурные направления

Single-node остаётся полноценным поддерживаемым вариантом. Multi-node/HA развивается через явные роли узлов, maintenance/drain/replacement/decommission, quorum/fencing/split-brain protection для stateful-профилей, контролируемый switchover/failover только после фактической сертификации и проверяемое восстановление после потери узлов или данных.

Managed Network является частью Core: multi-NIC, WAN/LAN роли, VLAN/bonding где поддерживается, routing, DNS/NTP, firewall policy и staged changes с connectivity validation и rollback. NAT/port-forwarding включаются только явно; наличие WAN+LAN не превращает узел в маршрутизатор автоматически.

Recovery включает Recovery Points, integrity metadata, изолированный restore, поддерживаемые PostgreSQL backup/PITR профили, object-level recovery, измеряемые RPO/RTO и регулярные restore drills. Наличие backup без проверенного restore не считается доказанной готовностью восстановления.

Capacity Planner должен оценивать безопасно доступные ресурсы, прогнозировать исчерпание резерва, выявлять bottleneck и выдавать what-if/placement рекомендации. Автоматическое применение допускается только в отдельно опубликованной policy-driven границе.

## Core и Market

Core содержит Identity/RBAC, Desired/Actual State, Changes/Jobs, Node/Role/Lifecycle, Network, Monitoring/Health, Audit, Backup/Recovery contracts, Capacity Planner foundation и системные API/security boundaries.

Market содержит устанавливаемые инфраструктурные модули. Для каждого модуля обязательны identity, compatibility/dependency metadata, permissions/capabilities, network/storage requirements, capacity profile, lifecycle и проверяемая license/compliance metadata. Приоритетные семейства: Directory Services, DNS/DHCP, PXE Windows/Linux, Software Automation Windows/Linux, IT Asset/Software Inventory, File Services, Monitoring и Backup.

## Безопасные изменения и recovery

Для опасной операции интерфейс и документация обязаны указывать: что изменится, риск/blast radius, preflight, проверку результата и rollback/recovery. Stateful workload нельзя переносить как stateless сервис без provider-specific migration/recovery adapter и проверки состояния данных.

## Аутентификация

После чистой установки создаётся локальный `admin` с первоначальным паролем `admin`. Первый вход требует обязательной смены пароля; до неё обычная работа запрещена. Обновление сохраняет заданный пользователем пароль и никогда не сбрасывает его к первоначальному значению.

## Критерии готовности следующего capability/release

Capability считается готовой только при наличии проверяемых data/API contracts, RBAC, failure/recovery model, stale-state/idempotency boundaries, health/audit semantics, negative/security tests, upgrade/migration path, backup/restore semantics для stateful data и актуальной эксплуатационной документации.

Следующий Stable не публикуется при release-blocking defect, отсутствии проверяемого acceptance, неопределённом install/upgrade/recovery path, расхождении документации с кодом или нерешённой high-risk security/recovery проблеме.
