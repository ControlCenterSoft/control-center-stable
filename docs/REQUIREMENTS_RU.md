# Control Center — принятые требования

Статус: **нормативный каталог требований для разработки**.

Назначение документа — не заменять детальную архитектуру, а не дать потеряться уже принятым требованиям между релизами и параллельными ветками. Порядок реализации определяет `ROADMAP.md`, архитектурные способы реализации — `ARCHITECTURE.md`.

## R-ROLE — Роли и топология

- R-ROLE-001: один сервер может одновременно выполнять несколько ролей Control Center;
- R-ROLE-002: обязательны Management Node, Global Controller, Site Controller, Controller Cluster Member, Worker Node, Managed Node и Agent contracts;
- R-ROLE-003: Data/Consensus/Repository/Telemetry/Backup/Edge роли являются логическими и могут совмещаться на малом сервере;
- R-ROLE-004: иерархия управления многоуровневая и не привязана к фиксированной организационной структуре;
- R-ROLE-005: один логический Control Plane может иметь несколько физических членов, но не несколько независимых masters одного scope;
- R-ROLE-006: Site Controller обеспечивает автономную работу площадки при потере WAN;
- R-ROLE-007: Physical Node не равен Service Identity; перенос роли не меняет логическую идентичность сервиса;
- R-ROLE-008: Placement решения учитывают текущую/прогнозную нагрузку и резерв.

## R-ENR — Enrollment и Agent

- R-ENR-001: первая установка создаёт Seed Controller;
- R-ENR-002: новые узлы подключаются через bootstrap token, remote SSH/WinRM, PXE или Offline Bundle;
- R-ENR-003: token одноразовый и короткоживущий;
- R-ENR-004: узел получает уникальный сертификат и использует mTLS;
- R-ENR-005: Agent передаёт inventory, heartbeat, Actual State, telemetry/events и выполняет только типизированные действия;
- R-ENR-006: Site может кэшировать пакеты, чтобы дочерним узлам не требовался прямой Internet access.

## R-STATE — Распределённое состояние

- R-STATE-001: Desired State и Actual State разделены;
- R-STATE-002: Desired/policy/config преимущественно идут сверху вниз, Actual/inventory/events/results/telemetry — снизу вверх;
- R-STATE-003: Global↔Site использует явный State Synchronization Protocol, а не растянутую через WAN physical DB replication;
- R-STATE-004: распределённые объекты имеют scope/owner/generation/resource_version;
- R-STATE-005: глобальная политика может разрешать ограниченные local overrides;
- R-STATE-006: критические данные синхронизируются оперативно, некритичная телеметрия/история — по управляемому интервалу.

## R-DATA — Данные и БД

- R-DATA-001: основная транзакционная БД Control Center — PostgreSQL с первого релиза;
- R-DATA-002: целевой HA PostgreSQL provider — Patroni; coordination/leader-election слой — etcd;
- R-DATA-003: Data Node является отдельной логической ролью и может быть вынесен на отдельное железо по capacity recommendation;
- R-DATA-004: большие пакеты/ISO/backups не хранятся как основной BLOB workload в product PostgreSQL;
- R-DATA-005: тяжёлая telemetry/time-series имеет отдельный backend abstraction и retention;
- R-DATA-006: Site может иметь локальный State Store для автономной работы.

## R-CAP — Capacity Planner

- R-CAP-001: показывать Current Devices, Safe Capacity, technical limit/confidence и reserve;
- R-CAP-002: учитывать CPU, RAM, storage capacity, IOPS, latency, queue depth, DB, network bandwidth/latency/loss и service queues;
- R-CAP-003: учитывать тип и интенсивность обслуживаемых устройств через Device Workload Profiles;
- R-CAP-004: каждый официальный Market-модуль имеет Capacity Profile;
- R-CAP-005: модель калибруется фактической телеметрией конкретной установки;
- R-CAP-006: поддерживать trend forecast и прогноз срока исчерпания резерва;
- R-CAP-007: поддерживать what-if analysis;
- R-CAP-008: выдавать конкретную рекомендацию: добавить/перенести роль, увеличить storage/network либо изменить workload policy;
- R-CAP-009: резерв должен учитывать отказ узла и эксплуатационные пики, а не разрешать 100% загрузку.

## R-LOAD — Нагрузочные тесты

- R-LOAD-001: создать Synthetic CC Agent Generator;
- R-LOAD-002: поддерживать ступенчатое наращивание нагрузки до насыщения;
- R-LOAD-003: измерять CPU/RAM/storage/DB/network/API percentiles/queues/errors/job duration;
- R-LOAD-004: тестировать degraded WAN, packet loss/latency, disk pressure и component failure;
- R-LOAD-005: результат официального теста сохраняется как воспроизводимый Capacity Profile;
- R-LOAD-006: capacity claim без тестовой или фактической evidence не является сертифицированным.

## R-LCM — Жизненный цикл серверов

- R-LCM-001: Node Lifecycle включает Discover/Enroll/Active/Drain/Maintenance/Update/Replace/Remove/Retire и Failed/Recovering paths;
- R-LCM-002: Maintenance начинается только после preflight и Drain;
- R-LCM-003: Replace Node сначала вводит новый узел, синхронизирует state/workloads, выполняет switchover/health-check и только затем retire старого;
- R-LCM-004: Data Node переносится через DB-aware replica/switchover, а не копированием файлов;
- R-LCM-005: роль/сервис должен быть восстанавливаем из Desired State там, где это возможно.

## R-UPD — Обновление

- R-UPD-001: Upgrade Orchestrator строит dependency graph;
- R-UPD-002: preflight проверяет quorum, DB replicas/lag, backup/recovery point, disk, compatibility, capacity reserve и active jobs;
- R-UPD-003: кластер обновляется rolling-порядком, лидер/primary — после followers/replicas и controlled switchover;
- R-UPD-004: поддерживаются Canary/Early/Standard/Final rings;
- R-UPD-005: поддерживаются maintenance windows, pause/stop, failure threshold и change freeze;
- R-UPD-006: target compatibility — Controller N с как минимум Agent/Site N и N-1 в заявленной матрице.

## R-REC — Backup/Recovery/DR

- R-REC-001: Recovery Manager восстанавливает Node/Service/DB/Config/User/Group/Policy/Application/Site/Control Plane;
- R-REC-002: stateful failover требует fencing;
- R-REC-003: потеря quorum переводит Control Plane в safe/degraded mode вместо split-brain;
- R-REC-004: PostgreSQL backup поддерживает base backup + WAL/PITR;
- R-REC-005: object-level restore не должен требовать отката всех корректных изменений после ошибки;
- R-REC-006: критичные объекты поддерживают soft-delete/Recycle Bin и history;
- R-REC-007: массовое destructive действие создаёт Recovery Point до применения;
- R-REC-008: RPO/RTO задаются политикой и проверяются фактическим restore drill;
- R-REC-009: Backup Health означает проверенную возможность восстановить данные;
- R-REC-010: Stateful Market module имеет Recovery Adapter.

## R-NET — Сеть и защита

- R-NET-001: Control Center Core управляет несколькими NIC, VLAN и network zones;
- R-NET-002: типовые зоны: WAN/LAN/MANAGEMENT/DMZ/CLUSTER/STORAGE/BACKUP/TRUSTED;
- R-NET-003: WAN+LAN не включает IP forwarding/routing автоматически;
- R-NET-004: Routing/NAT/Port Forwarding доступны только через явную Edge Gateway policy;
- R-NET-005: Linux firewall baseline — nftables через структурированный backend;
- R-NET-006: service/Market manifest декларирует необходимые network requirements, но policy решает, что реально открыть;
- R-NET-007: IP/route/firewall changes применяются staged с connectivity check и auto-rollback;
- R-NET-008: network capacity включена в Capacity Planner.

## R-MKT — Market Manifest и lifecycle

Каждый production-ready Market module обязан поддерживать применимые стадии:

`Install → Configure → Health → Capacity → Scale/Placement → Update → Migrate/Drain → Backup → Restore → Failover → Remove`.

Manifest также описывает dependencies, ports/zones, storage, secrets, workload type, permissions, risk, provider/backend и compatibility.

Workload types как минимум: STATELESS, STATEFUL, CLUSTERED, SINGLETON.

## R-MAIL — Mail & Groupware

- полный email/SMTP/IMAP/Webmail;
- calendars/shared calendars;
- contacts/address books;
- mobile synchronization;
- anti-spam;
- SPF/DKIM/DMARC/TLS;
- quotas/aliases/groups;
- directory integration;
- DNS readiness checks: MX/A/AAAA/PTR/SPF/DKIM/DMARC/port reachability;
- HA/capacity/backup/recovery;
- первый provider profile: Postfix+Dovecot+Rspamd+SOGo;
- альтернативный backend допускается через provider abstraction;
- Internet-facing Mail Edge рекомендуется в DMZ.

## R-1C — 1C:Enterprise Server

- сервер/кластер 1С, worker processes, RAS, information bases;
- DB integration с проверкой официальной совместимости;
- Web publishing где применимо;
- licensed distribution предоставляет пользователь;
- monitoring/capacity/HA/update/backup/restore/migration;
- БД 1С логически и эксплуатационно отделена от DB Control Center;
- capacity учитывает active/concurrent sessions, CPU/RAM, DB latency/locks, storage latency и background jobs.

## R-SWG — Secure Web Gateway / Corporate Proxy

- Explicit Proxy/PAC;
- Transparent/Intercept/TPROXY;
- hybrid mode;
- AD/LDAP/Kerberos identity для explicit режима;
- device/IP/VLAN/Site identity для transparent режима;
- URL/category allow/block policies;
- schedules;
- bandwidth limits и quotas;
- accounting/reports;
- configurable retention/RBAC для proxy logs;
- optional TLS inspection, по умолчанию OFF, с исключениями;
- pluggable AV/ICAP/DLP;
- HA/capacity;
- первый provider profile: Squid 7.x+nftables/TPROXY+traffic control;
- Explicit Proxy может работать на Worker Node; transparent deployment интегрируется с Edge Gateway/redirect path.

## R-ADM — Первый администратор

- после новой установки создаётся локальный `admin` с одноразовым начальным паролем `admin`;
- первый вход разрешает только смену пароля/проверку сессии/выход;
- обычная работа до смены пароля запрещена;
- update не сбрасывает существующий пароль и не восстанавливает bootstrap credential.

## Критерий полноты

Новая возможность считается полностью реализованной только если для неё определены и проверены: данные/API, RBAC, Actual/Desired State, Audit/Health, update/migration, failure/recovery, capacity (если применимо), документация и acceptance tests.
