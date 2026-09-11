# Архитектура Control Center

Статус: **нормативный архитектурный источник истины для текущей разработки**.

Этот документ описывает целевую архитектуру продукта. Если текущая реализация ещё не поддерживает описанную возможность, код считается переходным состоянием и должен эволюционировать к этой модели без создания параллельных несовместимых контрактов.

## 1. Базовые принципы

Control Center развивается как модульный монолит с жёсткими доменными границами. Вынос компонента в отдельный процесс или узел выполняется только по измеренной нагрузке, независимому жизненному циклу, требованиям отказоустойчивости или отдельной границе доверия.

Канонический путь изменения состояния:

`Запрос → Валидация → Авторизация → План → Change → Job → типизированное действие → Проверка → Actual State → Audit`

Обязательные инварианты:

- RBAC — deny-by-default и проверяется на сервере;
- Web/API не предоставляет произвольный shell/exec;
- длительные изменения выполняются через Change/Job;
- Jobs устойчивы, идемпотентны и имеют явную модель повторов/lease;
- Desired State отделён от Actual State;
- все опасные операции имеют модель отказа и восстановления;
- PostgreSQL хранит транзакционное состояние Control Center;
- тяжёлая телеметрия, большие артефакты и резервные копии не должны бесконечно расти в основной транзакционной БД.

## 2. Источник истины разработки

Для реализации источником истины является ветка `main` репозитория `ControlCenterSoft/control-center-development` совместно с тремя нормативными файлами:

- `ARCHITECTURE.md` — архитектура и инварианты;
- `ROADMAP.md` — последовательность внедрения;
- `docs/REQUIREMENTS_RU.md` — принятые требования и критерии полноты.

Репозиторий `control-center-stable` является стабильным релизным каналом, а `control-center` — публичным сайтом/витриной. Они не определяют архитектуру разработки.

Документация Google Drive является подробной продуктовой и эксплуатационной документацией и должна синхронизироваться с этим источником истины. При обнаружении расхождения реализация не должна продолжать развитие спорного контракта до устранения расхождения.

## 3. Ролевая модель узла

Базовая сущность — **Control Center Node**. Роли не являются взаимоисключающими: один физический сервер может одновременно выполнять несколько функций, если Capacity Planner подтверждает достаточный резерв.

Нормативные роли и понятия:

- **Management Node / Сервер управления** — узел с API и Web UI управления;
- **Global Controller / Глобальный контроллер** — корневой логический Control Plane;
- **Site Controller / Контроллер площадки** — автономный контроллер подразделения, площадки или изолированного контура;
- **Controller Cluster Member / Член кластера управления** — физический участник одного логического Control Plane;
- **Cluster Coordinator / Координатор кластера** — выбранный кластером координатор/лидер, а не постоянный ручной master;
- **Worker Node / Исполнительный сервер** — узел выполнения сервисов, заданий и Market workloads;
- **Managed Node / Управляемый узел** — управляемый сервер/устройство;
- **Control Center Agent** — минимальный доверенный агент узла;
- **Management Zone / Зона управления** — логическая область политик, RBAC и сетевых/эксплуатационных ограничений;
- **Data Node / Узел данных** — PostgreSQL для состояния Control Center;
- **Consensus Node / Узел консенсуса** — quorum/leader election/locks;
- **Repository Node / Узел репозитория** — пакеты, установщики, PXE/ISO/Market-артефакты;
- **Telemetry Node / Узел телеметрии** — масштабируемое хранение/обработка метрик;
- **Backup Repository Node / Узел резервного хранения** — резервные копии, WAL/snapshots и DR-артефакты;
- **Edge Gateway / Пограничный шлюз** — явная роль маршрутизации/NAT/firewall; наличие WAN+LAN само по себе эту роль не включает.

Физический сервер не равен сервисной идентичности: роль или сервис должны переноситься на другой узел без изменения своей логической идентичности.

## 4. Иерархия и кластер управления

Иерархия универсальна и допускает несколько уровней вложенности. Не вводится жёсткая обязательная роль `Regional Controller`: любой Site/Management scope может иметь дочерние scopes/контроллеры.

Один логический родительский Control Plane может состоять из нескольких физических Controller Cluster Members. Несколько независимых masters одного scope запрещены.

Поддерживаемые профили:

- 1 Controller — standalone;
- 2 Controllers + Witness — допустимый HA-профиль;
- 3 Controllers — рекомендуемый HA-профиль;
- 5 Controllers — крупный профиль.

Критические записи запрещены при потере quorum. Оставшийся меньшинством узел переходит в безопасный/ограниченный режим вместо риска split-brain.

Site Controller обязан продолжать разрешённые локальные операции при потере WAN: локальный UI/API, политики, Jobs, inventory, локальные Market-сервисы и кэш. После восстановления связи выполняется контролируемая синхронизация.

## 5. Enrollment и распространение Control Center

Первая интернет-установка создаёт **Seed Controller**. После неё новые узлы могут получать Control Center из уже работающей инфраструктуры.

Control Center Agent поддерживает:

- уникальный Node ID;
- heartbeat/freshness;
- hardware/network/software inventory;
- capability и role negotiation;
- типизированное выполнение задач;
- установку/обновление управляемых сервисов;
- получение Desired State;
- отправку Actual State/Events/Telemetry;
- self-update по контролируемой политике.

Поддерживаемые способы присоединения:

1. bootstrap-команда + одноразовый enrollment token;
2. remote bootstrap через SSH/WinRM;
3. PXE/автоматическая установка;
4. Offline Enrollment Bundle для air-gap.

Enrollment использует короткоживущий одноразовый token, fingerprint/доверие к CA, уникальный сертификат узла и mTLS. Постоянный bootstrap-пароль запрещён.

## 6. Desired/Actual State и синхронизация

Сверху вниз передаются Desired State, политики, RBAC, конфигурация, разрешённые приложения и ограничения.

Снизу вверх передаются Actual State, inventory, telemetry, события, результаты Jobs, ошибки и capacity observations.

Критические данные синхронизируются практически в реальном времени. Некритичные inventory/telemetry/history могут иметь настраиваемый интервал.

Global и Site не должны строиться как одна физически растянутая через WAN PostgreSQL-репликация. У автономного Site допускается локальный State Store, а связь Global↔Site реализуется явным **Control Center State Synchronization Protocol** с ownership/version/conflict rules.

Нормативные поля распределённого объекта включают как минимум:

`object_id`, `scope_id`, `owner_scope`, `generation`, `resource_version`, `created_at`, `updated_at`.

Глобальные политики и глобальный Desired State принадлежат верхнему scope; локальный Actual State, inventory/events/jobs/telemetry — соответствующему Site. Локальные overrides допускаются только в явно делегированных границах.

## 7. Данные, PostgreSQL и HA

PostgreSQL остаётся основной транзакционной БД с первого сервера.

В малой установке Controller, Data Node, Repository и Worker могут находиться на одном сервере. При росте роли разносятся без изменения контрактов приложения.

Для HA целевая схема:

- PostgreSQL + Patroni для управления репликами/switchover/failover;
- etcd как целевой consensus/DCS слой для leader election и coordination;
- Data Node и Consensus Node — логические роли, не обязательные отдельные физические машины в малой установке.

Primary DB не используется как хранилище больших ISO/пакетов/backup BLOB. Для этого существует Repository/Object Storage abstraction. Тяжёлая time-series телеметрия должна иметь отдельный backend abstraction и retention.

## 8. Capacity & Placement Advisor

Control Center обязан не только показывать загрузку, но и рассчитывать:

- текущее число обслуживаемых устройств;
- рекомендуемую безопасную ёмкость;
- технический предел с указанием низкой надёжности такой оценки;
- текущий bottleneck;
- запас по CPU/RAM/storage/IOPS/latency/network/DB/service queues;
- прогноз исчерпания резерва;
- влияние изменения настроек (`what-if`);
- рекомендацию добавить/перенести конкретную роль или сервис.

Capacity Planner использует hardware inventory, фактическую телеметрию, Device Workload Profiles, Market Capacity Profiles, DB/network/storage characteristics, историю роста и результаты нагрузочных тестов. Модель должна самокалиброваться на реальной инфраструктуре.

Каждый официальный Market-модуль обязан иметь Capacity Profile и benchmark/load-test scenario.

## 9. Нагрузочные испытания

Нормативный тестовый контур должен уметь синтетически моделировать сотни и тысячи CC Agents и повышать нагрузку ступенями (например 100→500→1000→2500→5000→10000 и далее до насыщения).

Измеряются CPU, RAM, storage latency/IOPS/queue, DB latency/TPS/WAL, network throughput/loss/latency, API P50/P95/P99, длина очередей, job completion time и error rate.

Допускаемые инструменты: k6/Locust, pgbench, fio, iperf3, stress-ng, tc/netem и собственный Synthetic CC Agent Generator. Результат теста — воспроизводимый Capacity Profile, а не маркетинговая цифра максимума.

## 10. Node Lifecycle Manager

Жизненный цикл узла является частью Core:

`DISCOVERED → ENROLLING → ACTIVE → DRAINING → MAINTENANCE → UPDATE/REPLACE/REMOVE → ACTIVE/RETIRED`

Аварийный путь:

`ACTIVE → FAILED → RECOVERING → ACTIVE/REPLACED`.

Drain запрещает новое размещение/Jobs на узле, завершает или переносит активные операции и мигрирует переносимые сервисы перед обслуживанием.

Replace Node сначала enroll/проверяет новый сервер, синхронизирует необходимые сервисы/данные, выполняет switchover и только после health-check выводит старый сервер в RETIRED.

## 11. Upgrade Orchestrator

Обновление выполняется по dependency graph, а не командой «обновить всё».

Preflight обязан проверять quorum, DB replicas/lag, резервную копию и recovery point, свободное место, совместимость версий, capacity reserve, активные критические Jobs и состояние зависимостей.

Кластер обновляется rolling-схемой: followers/replicas сначала, leader/primary — после контролируемого switchover. Поддерживаются update rings: Canary → Early → Standard → Final, maintenance windows, pause/stop и change freeze.

Целевой compatibility contract: Controller версии N управляет как минимум Site/Agent N и N-1 в пределах объявленной матрицы совместимости.

## 12. Recovery Manager и отказоустойчивое восстановление

Recovery Manager покрывает восстановление Node, Service, Database, Configuration, User/Group, Policy, Application, Site и всего Control Plane.

Обязательные возможности:

- fencing перед stateful failover;
- восстановление Worker workload на другом узле;
- безопасная деградация при потере quorum;
- rebuild Controller/Site из Desired State;
- логические controller endpoints вместо жёстко прошитого IP в Agents;
- PostgreSQL base backup + WAL/PITR;
- объектное восстановление без обязательного отката всей production DB;
- soft delete/Recycle Bin и история версий для критичных объектов;
- восстановление связей пользователя/групп/RBAC/политик;
- автоматический Recovery Point перед high-impact mutation;
- настраиваемые RPO/RTO;
- регулярный изолированный restore drill с функциональной проверкой;
- Backup Health означает доказанную восстанавливаемость, а не только успешное завершение backup job.

Для PostgreSQL целевой backup provider — pgBackRest или совместимый provider через abstraction. Stateful Market-модули обязаны иметь собственный Recovery Adapter.

Типизированные контракты метаданных RecoveryPoint/Backup/Restore, evidence, provider и fencing зафиксированы в [`docs/RECOVERY_METADATA_CONTRACTS_RU.md`](docs/RECOVERY_METADATA_CONTRACTS_RU.md) и [`api/openapi-recovery-metadata.yaml`](api/openapi-recovery-metadata.yaml). Контракты не запускают backup/restore и не выполняют инфраструктурные изменения.

## 13. Network & Security Manager

Сетевые интерфейсы и зоны — часть Core. Узел может иметь WAN, LAN, MANAGEMENT, DMZ, CLUSTER, STORAGE, BACKUP и иные интерфейсы/VLAN.

Наличие WAN+LAN **не включает маршрутизацию автоматически**. По умолчанию межзонная пересылка запрещена. Routing/NAT/port-forwarding включаются только при назначении Edge Gateway и явной policy.

Linux firewall baseline — nftables через управляемый структурированный backend (libnftables/JSON или эквивалент), без генерации произвольного shell из Web UI.

Сетевые изменения применяются безопасно:

`Recovery snapshot → временное применение → connectivity check → confirm`;

при потере управляющего соединения выполняется автоматический rollback.

Netplan может быть одним из OS backends, но публичный Core contract не привязан к конкретному дистрибутиву.

## 14. Market Manifest v2

Текущий минимальный manifest `install/upgrade/remove` является переходным. Целевой контракт каждого серьёзного модуля включает:

- install;
- configure;
- health/diagnostics;
- capacity profile;
- scale/placement requirements;
- update;
- migrate/drain;
- backup;
- restore;
- failover;
- remove;
- dependency/network/storage/secret requirements;
- security permissions/risk metadata;
- provider/backend abstraction.

Workload type должен различать как минимум STATELESS, STATEFUL, CLUSTERED и SINGLETON.

Новый Market workload не считается production-ready без Update, Backup/Restore, Health, Capacity и failure-path tests.

Исполняемый контракт полей, правил валидации, явной активации и консервативной миграции v1→v2 зафиксирован в [`docs/MARKET_MANIFEST_V2_RU.md`](docs/MARKET_MANIFEST_V2_RU.md) и [`api/openapi-market-manifest-v2.yaml`](api/openapi-market-manifest-v2.yaml).

## 15. Обязательные корпоративные модули Market

Помимо уже существующих направлений Directory Services, DNS/DHCP, PXE, Automation, Inventory, File Services и Monitoring в целевой Market входят:

### Mail & Groupware

Полноценная почта, SMTP/IMAP, Webmail, календари, общие календари, контакты/адресные книги, мобильная синхронизация, anti-spam, SPF/DKIM/DMARC, TLS, quotas, directory integration, HA, capacity и backup/recovery.

Первичный provider profile: Postfix + Dovecot + Rspamd + SOGo. Архитектура допускает альтернативный provider, например Stalwart, без изменения пользовательского контракта. Internet-facing Mail Edge рекомендуется размещать в DMZ и не совмещать с Global Controller при наличии ресурсов.

### 1C:Enterprise Server

Управление сервером/кластером 1С, рабочими процессами/RAS, информационными базами, совместимым DB provider, Web publication, лицензированным дистрибутивом пользователя, monitoring/capacity, HA, update, backup/restore и migration.

БД 1С всегда логически и операционно отделена от PostgreSQL Control Center, даже при временном размещении на одном физическом сервере.

### Secure Web Gateway / Corporate Proxy

Корпоративный прокси-шлюз с Explicit Proxy/PAC и Transparent/Intercept/TPROXY режимами, directory authentication для explicit режима, device/IP/VLAN/Site identity для transparent режима, URL/category policies, allow/block lists, schedules, bandwidth limits, quotas/accounting, отчёты, optional TLS inspection (по умолчанию OFF), pluggable antivirus/ICAP/DLP, HA и Capacity Profile.

Первичный provider profile: Squid 7.x + nftables/TPROXY + Linux traffic control + Control Center Policy Engine. Backend должен оставаться заменяемым.

Transparent mode обычно требует Edge Gateway или управляемого redirect path; Explicit Proxy может работать на обычном Worker Node.

## 16. Порядок внедрения

Архитектурные контракты ролей, scope/site/zone, Node lifecycle, Market Manifest v2, network model, capacity/recovery metadata и distributed object versioning должны быть заложены **до дальнейшего массового расширения Market и до HA**.

Фактическая реализация etcd/Patroni, полноценного multi-site sync, автоматического rebalancing и Enterprise Market выполняется поэтапно согласно `ROADMAP.md`. Наличие целевого контракта сейчас не означает, что сложная HA должна появиться в одном релизе.
