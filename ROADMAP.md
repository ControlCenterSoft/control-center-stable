# Control Center — продуктовая дорожная карта и критерии готовности

Статус: **CURRENT**

## 1. Текущий релизный статус

Последний официально опубликованный canonical/source release Control Center — **0.25.0**. Полноценный **PUBLIC STABLE RELEASE 0.25.0** опубликован в официальном stable-канале [`ControlCenterSoft/control-center-stable`](https://github.com/ControlCenterSoft/control-center-stable) с актуальными stable/default branch, tag `v0.25.0`, официальным GitHub Release и предусмотренными artifacts/checksums/manifest/provenance.

Версия **0.26.0** является текущим release candidate и не считается пользовательски опубликованной до завершения собственного release cycle. Наличие кода, контракта, ветки или предварительной версии не означает пользовательскую доступность.

## 2. Уже опубликованные направления

Опубликованная линия до 0.25.0 включает:

- базовые Identity/RBAC/Audit и durable state boundaries;
- Changes/Jobs и типизированную модель операций;
- lifecycle/recovery contracts;
- Site/Network foundation;
- advisory Capacity Intelligence: forecast, what-if, placement advice, bottleneck/horizon/calibration и последующие resource-safety ограничения;
- Session Security Policy;
- read-only RBAC self-introspection текущей identity;
- bounded process-local защиту локального входа от brute force и credential spraying;
- permission-gated bounded read-only доступ к Audit events с bounded pagination, точными фильтрами, integrity validation и fail-closed Audit evidence.

Capacity-возможности остаются advisory-only и сами по себе не разрешают автоматическое изменение инфраструктуры.

## 3. Ближайшая кандидатная линия

Текущий candidate **0.26.0** — Audit Integrity: permission-gated read-only проверка целостности Audit с fail-closed поведением для persistence и HTTP boundary. До завершения qualification и официальной публикации этот scope остаётся кандидатным и не должен описываться как доступный в public stable.

Следующие capability обязаны сохранять совместимость с опубликованными Identity/RBAC, Change/Job, Audit, recovery и API boundaries. Нельзя объявлять кандидатную функцию опубликованной до официального релиза.

## 4. Single-node, multi-node и HA

Single-node является полноценным поддерживаемым способом использования продукта.

Целевая multi-node/HA модель включает:

- controller/worker/data/repository/telemetry/backup/edge роли там, где они действительно нужны;
- maintenance, drain, replacement и decommission;
- контролируемый перенос ролей и сервисов;
- quorum/fencing/split-brain protection для применимых stateful профилей;
- controlled switchover/failover только после фактической сертификации;
- восстановление после потери одного или нескольких узлов;
- проверяемое восстановление данных.

Наличие архитектурного контракта HA не является доказательством production failover. Возможность считается поддержанной только после соответствующих failure/recovery tests и публикации версии.

## 5. Lifecycle узлов и сервисов

Для узлов применяются состояния и операции enrollment, active, maintenance, drain, replacement и decommission/remove.

Для опасной операции обязательны:

- описание изменения;
- риск и blast radius;
- preflight;
- проверка результата;
- rollback/recovery или безопасная компенсация.

Stateful workload нельзя переносить как stateless сервис: необходим provider-specific migration/recovery adapter и проверяемое состояние данных.

## 6. Backup, restore и recovery

Recovery является отдельной продуктовой подсистемой. Целевая модель включает Recovery Points, integrity metadata, изолированный restore, PostgreSQL backup/PITR для поддерживаемых профилей, object-level recovery, измеряемые RPO/RTO и регулярные restore drills.

Backup без подтверждённого restore не считается доказанной готовностью восстановления.

## 7. Managed Network

Network Management является частью Core. Целевая модель должна поддерживать multi-NIC, назначаемые зоны, VLAN/bonding там, где доступно, routing, DNS/NTP, firewall policy и staged changes с connectivity verification.

NAT/port-forwarding включаются только явно. WAN+LAN конфигурация не должна автоматически превращать узел в маршрутизатор. Ошибочное сетевое изменение должно иметь automatic rollback или заранее определённый recovery path.

## 8. Core и Market

Core содержит обязательные платформенные функции:

- Identity/RBAC;
- Desired/Actual State;
- Changes/Jobs;
- Node/Role/Lifecycle;
- Network;
- Monitoring/Health;
- Audit;
- Backup/Recovery contracts;
- Capacity Planner foundation;
- системные API и общие security boundaries.

Market содержит устанавливаемые инфраструктурные возможности. Для каждого модуля обязательны identity, compatibility/dependency metadata, permissions/capabilities, network/storage requirements, capacity profile, lifecycle и внутренний legal/compliance metadata block: license/SPDX expression, authoritative source, distribution mode, commercial/redistribution disposition, notice/source-offer requirements и versioned evidence digest. При clean status этот механизм не должен добавлять отдельный обязательный пользовательский workflow.

Приоритетные семейства Market:

- Directory Services с поддерживаемыми providers, включая Samba AD и FreeIPA там, где применимо;
- DNS/DHCP;
- PXE Windows/Linux;
- Software Automation Windows/Linux;
- IT Asset Inventory и Software Inventory/Compliance;
- File Services;
- Monitoring;
- Backup и другие инфраструктурные providers через единый module contract.

Полный lifecycle модуля: Install → Configure → Health → Update → Migrate/Drain → Backup → Restore → Remove. Failover добавляется только для provider, где он реально поддержан и проверен.

## 9. Capacity Intelligence

Целевой Capacity Planner должен отвечать на три вопроса: сколько ресурсов безопасно доступно сейчас, когда закончится резерв и что конкретно рекомендуется изменить.

Направления развития:

- workload profiles;
- nonlinear capacity curves;
- DB/storage/network bottleneck analysis;
- self-calibration по фактической telemetry;
- прогноз исчерпания резерва;
- what-if для устройств и сервисов;
- рекомендации по добавлению/переносу ролей и увеличению ресурсов;
- confidence score и failure reserve.

Автоматическое применение рекомендации допускается только в отдельно опубликованной policy-driven границе и только для явно разрешённых workloads.

## 10. Аутентификация после чистой установки

Для публичного stable **0.25.0** после чистой установки создаётся локальный пользователь `admin` с первоначальным паролем `admin`. Первый вход обязан привести к смене пароля; до смены обычная работа запрещена. При обновлении пользовательский пароль сохраняется и не сбрасывается к первоначальному значению.

Для первой подходящей будущей версии зафиксировано изменение bootstrap-механизма: clean install должен генерировать уникальный криптографически стойкий одноразовый пароль для `admin`, хранить его локально только в `/root/control-center-bootstrap-password` с `root:root` и mode `0600`, не выводить credential в logs/Audit/telemetry/support/public artifacts, требовать смену до обычной работы и удалять bootstrap-файл после успешной смены. Обновление не должно генерировать новый bootstrap credential и не должно сбрасывать пользовательский пароль. Эта будущая политика не должна приписываться уже выпущенному 0.25.0.

## 11. Критерии готовности capability

Capability готова только если определены и проверены:

1. object/data/API contract;
2. RBAC permissions/scopes;
3. Desired/Actual semantics для изменяющей state функции;
4. failure/recovery model;
5. validation, stale-state protection и idempotency;
6. health/observability/audit semantics;
7. negative/failure/security tests по уровню риска;
8. upgrade/migration path;
9. backup/restore semantics для stateful data;
10. пользовательская и эксплуатационная документация;
11. соответствие фактической реализации заявленному поведению.

## 12. Критерии готовности релиза

Релиз нельзя считать завершённым, если остаётся release-blocking defect, отсутствует проверяемый acceptance, не определён install/upgrade/restore path для заявленной области, release notes расходятся с кодом, пользовательская документация выдаёт будущую функцию за опубликованную либо остаётся неразрешённая high-risk security/recovery проблема.

Canonical/source release и PUBLIC STABLE RELEASE — разные стадии. Публично доступной stable-версией считается только релиз, для которого подтверждены официальный stable/default branch, version tag, GitHub Release с `draft=false` и `prerelease=false`, а также предусмотренные public artifacts/manifest/checksums/provenance. Сам canonical release недостаточен.

## 13. Границы продукта

Control Center — самостоятельный инфраструктурный продукт для администраторов. Другие продукты не являются обязательными runtime-компонентами Control Center.

Продуктовая документация не должна содержать внутренние процессы разработки, служебные адреса, секреты, ключи, персональные данные или иную внутреннюю operational information.
