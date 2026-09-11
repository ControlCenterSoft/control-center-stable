# Дорожная карта Control Center

Статус: **Distributed Core Contracts 0.4 завершены; первый незакрытый этап — 0.5 Multi-node Operations и Lifecycle**.

Версионные номера ниже — целевые архитектурные пакеты, а не обещание календарной даты. Конкретный релиз публикуется только после прохождения его acceptance gates.

## 0. Текущая точка

В `main` уже существуют фундаментальные элементы Core: PostgreSQL persistence, Identity/RBAC/Audit, Changes/Jobs, типизированные действия, Agent enrollment/heartbeat, Inventory, Market manifests, PXE/Automation/Domain/Integration foundations и Web/API state surfaces. Contract Checkpoint 0.4 закрыт: распределённые envelope/object contracts, roles/scopes/sites/zones, Desired/Actual State, Node lifecycle, Market Manifest v2, Network, Capacity и Recovery metadata имеют совместимые schemas, migration path и qualification gates.

Текущие реализации Agent/Market/Node schemas считаются **переходными v1-контрактами**. Их не нужно выбрасывать: они должны быть расширены до принятой распределённой модели.

Правило следующего шага: дальнейшие Task Packets выбираются из 0.5, начиная с Agent runtime/bootstrap и role assignment. Они обязаны использовать контракты 0.4 и сохранять green CI.

## 1. 0.4 — Distributed Core Contracts

Цель: заложить данные и API так, чтобы последующие Site/HA/Capacity/Network функции не потребовали ломать Core.

Обязательные задачи:

- Node Role model: Management/Controller/Worker/Data/Consensus/Repository/Telemetry/Backup/Edge;
- Site, Management Zone, hierarchical scopes и delegated RBAC boundaries;
- `object_id/scope_id/owner_scope/generation/resource_version`;
- расширение Agent enrollment: roles, site/zone, hardware/network inventory, identity/certificate metadata;
- Desired State / Actual State ownership contract;
- Node lifecycle state machine;
- Market Manifest v2 schema с lifecycle/capacity/recovery/network/dependency metadata;
- Network Interface/Zone data model, пока без обязательной маршрутизации;
- CapacityObservation/CapacityProfile/Constraint/Recommendation schemas;
- RecoveryPoint/Backup/Restore metadata contracts;
- PostgreSQL migrations и API/OpenAPI для новых сущностей;
- обратная совместимость с текущими 0.3.x объектами или явная миграция.

Exit gate:

- clean install + upgrade from supported 0.3.x;
- old Node/Market objects migrate deterministically;
- API/schema tests PASS;
- ни один новый module family не создаёт собственные роли, scheduler, backup или network model.

## 2. 0.5 — Multi-node Operations и Lifecycle

Цель: один Controller безопасно управляет несколькими Worker/Managed Nodes.

Обязательные задачи:

- Control Center Agent runtime и role assignment;
- bootstrap command + one-time token;
- remote bootstrap через SSH/WinRM;
- Offline Enrollment Bundle;
- package/repository cache;
- placement planner v1;
- Node Lifecycle Manager: Drain/Maintenance/Replace/Remove;
- Upgrade Orchestrator: dependency graph, preflight, rolling order, update rings, maintenance window;
- безопасное service migration для stateless workloads;
- Data Node move plan только через replica/switchover abstraction;
- Capacity Planner MVP;
- Synthetic CC Agent load harness и базовые Capacity Profiles.

Exit gate:

- потеря/перезапуск Worker не создаёт false Success;
- Drain/Replace проверены failure injection;
- update canary может автоматически остановить rollout;
- Capacity Planner показывает safe capacity и bottleneck с указанной confidence.

## 3. 0.6 — Site Autonomy и Network Foundation

Цель: автономные подразделения и корректная работа при WAN loss.

Обязательные задачи:

- Site Controller;
- многоуровневая hierarchy без жёсткого Regional role;
- локальный Site State Store;
- Control Center State Synchronization Protocol;
- top-down Desired State / bottom-up Actual State, ownership/conflict rules;
- local policy overrides только в делегированных границах;
- offline local UI/jobs/market operations;
- Site repository cache;
- Network & Security Manager runtime;
- WAN/LAN/MANAGEMENT/DMZ/CLUSTER/STORAGE/BACKUP zones;
- nftables backend;
- safe staged IP/route/firewall changes + connectivity auto-rollback;
- Edge Gateway: routing/NAT/port-forwarding только после явного назначения;
- network metrics включаются в Capacity Planner.

Exit gate:

- Site продолжает разрешённую работу при разрыве WAN;
- reconnect не создаёт silent conflict;
- WAN+LAN без Edge Gateway не маршрутизируются;
- ошибочное firewall/network изменение автоматически откатывается.

## 4. 0.7 — HA Data/Control Plane и Disaster Recovery

Цель: доказанная отказоустойчивость, а не декларация HA.

Обязательные задачи:

- Controller cluster membership и quorum;
- etcd consensus/DCS layer;
- PostgreSQL HA через Patroni provider;
- profiles 1 / 2+Witness / 3 / 5 Controllers;
- controlled leader/primary switchover;
- fencing и split-brain protection;
- Backup Repository Node;
- PostgreSQL base backup + WAL/PITR provider;
- Recovery Manager;
- object-level recovery/Recycle Bin/change history;
- high-impact automatic Recovery Point;
- RPO/RTO policies;
- scheduled restore drills;
- full Controller/Site rebuild from Desired State;
- degraded safe/read mode при потере quorum.

Exit gate:

- Controller/DB node loss PASS;
- majority/quorum loss не допускает split-brain;
- PITR реально восстанавливается в изолированном контуре;
- случайно удалённые объекты можно восстановить без обязательного отката всей production DB;
- RPO/RTO измеряются фактом тестового восстановления.

## 5. 0.8 — Market Platform v2 и Enterprise Providers

Цель: единый зрелый lifecycle для всех Market workloads.

Сначала существующие модули переводятся на Manifest v2:

- Directory Services;
- DNS/DHCP;
- PXE Windows/Linux;
- Software Automation Windows/Linux;
- Inventory/Compliance;
- File Services;
- Monitoring.

После этого добавляются корпоративные providers:

### Mail & Groupware

Mail, Webmail, calendars, contacts, anti-spam, SPF/DKIM/DMARC, TLS, directory integration, HA/capacity/recovery. Первый provider profile: Postfix + Dovecot + Rspamd + SOGo; альтернативный backend допускается через общий контракт.

### 1C:Enterprise Server

1C server/cluster/RAS/information bases, отдельная DB, Web publishing, license-aware deployment, monitoring/capacity, HA, backup/restore/migration. Control Center не распространяет чужую лицензию и использует предоставленный пользователем лицензированный дистрибутив.

### Secure Web Gateway

Explicit/PAC и Transparent/TPROXY режимы, directory/device identity, category filtering, schedules, quotas, bandwidth control, accounting/reports, optional TLS inspection, pluggable AV/ICAP/DLP, HA/capacity. Первый provider profile: Squid 7.x + nftables/TPROXY + traffic control.

Exit gate для каждого модуля:

`Install → Configure → Health → Capacity → Update → Migrate/Drain → Backup → Restore → Failover (если заявлен) → Remove`

Все стадии имеют positive/failure/security tests.

## 6. 0.9 — Capacity Intelligence и масштабирование

Цель: перейти от мониторинга к прогнозированию.

- Device Workload Profiles;
- service-specific nonlinear capacity curves;
- DB/storage/network bottleneck analysis;
- self-calibration по реальной телеметрии;
- trend forecast и срок исчерпания резерва;
- what-if: число устройств, интервалы telemetry, новый Market workload, изменение WAN/storage;
- рекомендованный hardware profile для новой роли;
- one-click approved placement/migration;
- capacity test catalog для официальных модулей;
- confidence score и безопасный резерв с учётом отказа узла.

Exit gate:

Capacity Planner должен отвечать на три вопроса: **сколько безопасно обслуживаем сейчас, когда закончится резерв, что конкретно изменить**.

## 7. 1.0 — Policy-driven Operations

Только после накопления доказательной базы 0.5–0.9 допускаются:

- policy-driven automatic placement;
- automatic rebalance для явно разрешённых workloads;
- controlled automatic recovery;
- automatic scale recommendations/actions в заданных пределах;
- mature multi-site/HA/DR certification;
- production capacity baselines.

Автоматическое перемещение stateful workloads без provider-specific migration/recovery adapter запрещено.

## 8. Порядок внутри каждого релиза

Для любой новой capability:

1. contract/data/API/RBAC;
2. failure/recovery model;
3. implementation;
4. observability/audit;
5. load/failure/security tests;
6. upgrade/migration path;
7. documentation/runbook;
8. release evidence.

## 9. Что означает команда «продолжай разработку»

Если пользователь не задаёт более узкую цель, следующая работа выбирается из **первого незакрытого этапа этого ROADMAP**, с учётом текущего `main` и активных PR. Нельзя перепрыгивать к более позднему Enterprise/HA модулю, создавая контракт, который противоречит незакрытому фундаментальному этапу.
