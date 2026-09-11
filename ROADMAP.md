# Control Center — публичная дорожная карта и критерии готовности

Статус: **CURRENT**

## 1. Текущий релизный статус

Текущий официальный **PUBLIC STABLE RELEASE — 0.27.0**. Версия 0.26.0 сохраняется как предыдущая стабильная ступень. Наличие кода или предварительной реализации последующей возможности не означает её пользовательскую доступность: capability считается опубликованной только после собственного qualification/release cycle.

Public Stable 0.27.0 добавляет детерминированную read-only оценку актуальности подтверждённых изолированных restore drill. Freshness assessment связывается с точной restore metadata и digest проверочного evidence, возвращает bounded состояние `FRESH`, `STALE` или `UNVERIFIABLE` и работает fail-closed при невалидном или неподтверждаемом evidence. Возможность advisory/read-only и не выдаёт полномочий на backup, restore, fencing, retry или изменение production state. Опубликованные SQL migrations предыдущего Stable не изменяются.

## 2. Основные продуктовые инварианты

Control Center — самостоятельный инфраструктурный продукт. Single-node является полноценным поддерживаемым режимом. Multi-node/HA расширяет продукт только для фактически реализованных и квалифицированных ролей/providers.

Для state-changing операций действует общий путь:

`Запрос → Валидация → Авторизация → План → Change → Job → типизированное действие → post-condition verification → Actual State → Audit → recovery/rollback при необходимости`.

False Success запрещён: факт запуска команды или job не является доказательством успешного результата.

## 3. Аутентификация

После чистой установки действует обязательный bootstrap-flow локального администратора с принудительной сменой первоначального пароля до обычной работы. Обновление сохраняет установленный пользователем пароль и не возвращает систему к bootstrap-состоянию. Точные первоначальные учётные данные определены в инструкции установки соответствующего Stable.

## 4. Core и Market

**Core** содержит общие платформенные функции: Identity/RBAC, Desired/Actual State, Changes/Jobs, Node/Role/Lifecycle, Managed Network, Monitoring/Health, Audit, Backup/Recovery contracts, Capacity Planner foundation и системные API/security boundaries.

**Market** содержит устанавливаемые инфраструктурные возможности. Для каждого модуля обязательны identity, compatibility/dependencies, permissions/capabilities, network/storage requirements, capacity profile, lifecycle, backup/recovery semantics и legal/compliance metadata. Приоритетные семейства: Directory Services, DNS/DHCP, PXE Windows/Linux, Software Automation Windows/Linux, fleet/software inventory, File Services, Monitoring и Backup.

## 5. Lifecycle, backup и recovery

Для узлов предусмотрены enrollment, active, maintenance, drain, replacement и decommission. Для опасной операции всегда должны быть известны: что изменится, риск/blast radius, preflight, post-condition verification и rollback/recovery.

Stateful workload нельзя переносить как stateless сервис. Требуются provider-specific migration/recovery semantics и проверяемое состояние данных. Backup без подтверждённого restore не считается доказанной готовностью восстановления. Для критичных данных необходимы регулярные restore drills.

## 6. Managed Network

Network Management является частью Core и развивается как first-class subsystem: multi-NIC, WAN/LAN, zones, VLAN/bonding где поддерживается, routing, DNS/NTP, firewall, staged configuration changes, connectivity checks и automatic rollback.

WAN+LAN не превращает узел в маршрутизатор автоматически. NAT/port-forwarding/external publication включаются только явным действием и должны иметь собственные authorization/Audit/recovery boundaries.

## 7. Capacity Intelligence

Capacity Planner должен отвечать на три вопроса: сколько ресурсов безопасно доступно сейчас, когда закончится резерв и что рекомендуется изменить. Целевая модель включает workload profiles, CPU/RAM/storage/DB/network analysis, telemetry, growth trends, safe capacity, bottlenecks, forecasts, what-if и рекомендации по добавлению/переносу ролей и сервисов.

Capacity recommendations остаются advisory-only, пока отдельная опубликованная policy не разрешит безопасное автоматическое применение для конкретного workload.

## 8. Security и коммерческая готовность

Для каждой capability должны быть определены RBAC, stale-state/idempotency semantics, negative/failure/security tests, Audit, recovery, upgrade/migration и пользовательская документация. Для новых/изменяемых Market contracts требуется license/SPDX expression, authoritative source, distribution mode, commercial/redistribution disposition, notice/source-offer requirements и versioned evidence digest.

Публичная документация не должна содержать внутреннюю инфраструктуру или процессы разработки, служебные адреса, секреты, ключи, персональные данные или внутренние технические детали, не относящиеся к эксплуатации продукта.

## 9. Критерии готовности capability

Capability готова только если:

1. определены object/data/API contracts;
2. определены RBAC permissions/scopes;
3. state-changing функция имеет Desired/Actual semantics;
4. известна failure/recovery model;
5. проверены validation, stale-state protection и idempotency;
6. определены health/observability/Audit semantics;
7. выполнены negative/failure/security tests по уровню риска;
8. определён upgrade/migration path;
9. stateful data имеют backup/restore semantics;
10. пользовательская и эксплуатационная документация соответствует фактическому поведению.

## 10. Критерии готовности релиза

Релиз не считается завершённым при наличии release-blocking defect, отсутствии проверяемого acceptance, неготовом install/upgrade/restore path, несоответствии release notes коду, выдаче будущей функции за опубликованную или наличии неразрешённой high-risk security/recovery проблемы.

Public Stable подтверждается точной release identity, официальным version tag/release, предусмотренными artifacts/checksums/manifests/provenance и квалификацией соответствующего дерева. Следующие версии не считаются доступными заранее.
