# Контракт жизненного цикла узла Control Center 0.4

Статус: нормативный продуктовый контракт состояния узла для Distributed Core Contracts 0.4.

## 1. Назначение и границы

Объект `NodeLifecycle` хранит подтверждённое состояние одного физического Control Center Node и использует общий распределённый конверт:

- `object_id` — стабильный Node ID;
- `scope_id` и `owner_scope` — область размещения и владения;
- `generation` — поколение операторского Desired State;
- `resource_version` — непрозрачная версия каждой сохранённой редакции;
- `created_at`, `updated_at`, `state_changed_at` — временные границы объекта и текущего состояния.

Состояние `READY` является API-именем архитектурного состояния `ACTIVE`: enrollment завершён, идентичность проверена, readiness-проверки пройдены. Это не означает, что все необязательные сервисы узла работают без деградации.

Контракт не выполняет shell-команды, не меняет конфигурацию хоста и не переносит workloads. Он определяет допустимость изменения до того, как оркестратор и хранилище зафиксируют его.

## 2. Состояния и допустимые направления

| Состояние | Смысл | Допустимые следующие состояния |
|---|---|---|
| `DISCOVERED` | Узел обнаружен, но доверие ещё не установлено | `ENROLLING`, `RETIRED` |
| `ENROLLING` | Проверяются token, identity, certificate и readiness | `READY`, `DEGRADED`, `OFFLINE`, `RETIRED` |
| `READY` | Узел активен и допускает размещение/Jobs | `DEGRADED`, `DRAINING`, `OFFLINE` |
| `DEGRADED` | Узел доступен, но часть обязательных проверок не проходит | `READY`, `DRAINING`, `OFFLINE`, `RECOVERING` |
| `DRAINING` | Новое размещение запрещено, активная работа завершается или переносится | `MAINTENANCE`, `READY`, `DEGRADED`, `OFFLINE` |
| `MAINTENANCE` | Drain доказан; узел изолирован для обслуживания | `READY`, `UPDATING`, `REPLACING`, `REMOVING`, `DEGRADED`, `OFFLINE` |
| `UPDATING` | Выполняется контролируемое обновление | `READY`, `MAINTENANCE`, `DEGRADED`, `OFFLINE` |
| `REPLACING` | Подготовлен новый узел и выполняется switchover | `RETIRED`, `MAINTENANCE`, `DEGRADED`, `OFFLINE` |
| `REMOVING` | Выполняется подтверждённое удаление узла | `RETIRED`, `MAINTENANCE`, `DEGRADED`, `OFFLINE` |
| `OFFLINE` | Lease/heartbeat или связность потеряны | `RECOVERING`, `RETIRED` |
| `RECOVERING` | Идентичность восстановлена, идёт проверяемое восстановление | `READY`, `DEGRADED`, `OFFLINE`, `DRAINING` |
| `RETIRED` | Узел окончательно выведен из эксплуатации | нет; состояние терминальное |

Любое направление, отсутствующее в таблице, запрещено. В частности, запрещены прямые `READY → MAINTENANCE`, `READY → RETIRED` и любой выход из `RETIRED`.

Архитектурный аварийный путь `FAILED → RECOVERING` детализирован без неоднозначного общего `FAILED`: доступный, но неисправный узел имеет состояние `DEGRADED`, а потерявший lease/связность — `OFFLINE`. Оба пути допускают контролируемое восстановление.

## 3. Desired и Observation переходы

Каждое допустимое направление имеет фиксированный тип:

- `desired` — подтверждённое операторское намерение; `generation` увеличивается ровно на один;
- `observation` — результат health/readiness или подтверждение стадии операции; `generation` сохраняется.

`resource_version` меняется при каждом сохранённом переходе независимо от типа. Клиент не сравнивает и не сортирует это значение: версия непрозрачна.

Примеры:

- `READY → DRAINING` — `desired`: оператор начал обслуживание;
- `DRAINING → MAINTENANCE` — `observation`: планировщик доказал завершение Drain;
- `READY → OFFLINE` — `observation`: истёк heartbeat lease;
- `MAINTENANCE → UPDATING` — `desired`: одобрено обновление;
- `UPDATING → READY` — `observation`: обновление и readiness проверены.

Тип нельзя выбирать произвольно. Несоответствие типа направлению отклоняется.

## 4. Защитные evidence checks

Поле `passed_checks` принимает только известные типизированные проверки. Неизвестная, повторная или отсутствующая обязательная проверка делает переход недействительным.

Основные safety gates:

| Операция | Обязательные подтверждения |
|---|---|
| Начало enrollment | `enrollment-authorized` |
| Завершение enrollment | `identity-verified`, `readiness-passed` |
| Начало Drain | `maintenance-preflight-passed`, `scheduling-disabled` |
| Вход в Maintenance | свежий maintenance preflight, выключенный scheduling, `drain-complete`, отсутствие активных Jobs/placements и безопасность stateful workloads |
| Начало Replace | `operation-approved`, `replacement-node-ready`, `state-synchronized` |
| Завершение Replace | готовность нового узла, синхронизация, подтверждённый switchover и health-check замены |
| Начало Remove | одобрение удаления и полный набор доказательств Drain |
| Завершение Remove | `removal-verified`, `retirement-approved` |
| Возврат после Offline | проверенная identity, затем `recovery-verified` и `readiness-passed` |
| Retire недоверенного/offline узла | одобрение, отсутствие управляемого состояния и безопасность stateful workloads |

Отмена уже начатых Replace/Remove возвращает узел в `MAINTENANCE` только после `operation-cancelled` и `rollback-verified`; одного пользовательского запроса отмены недостаточно.

Наличие имени проверки в запросе не заменяет доверие к источнику. API/оркестратор обязан получить результат от соответствующего health, scheduler, job, data или recovery компонента и проверить права инициатора.

## 5. Атомарность и защита от гонок

Каждый запрос перехода содержит обязательный precondition:

- `object_id`;
- прочитанный клиентом `resource_version`;
- при необходимости прочитанный `generation`.

Хранилище должно в одной атомарной операции:

1. прочитать текущий объект;
2. сравнить precondition;
3. проверить направление, тип, timestamps, reason и evidence;
4. выделить новую непрозрачную `resource_version`;
5. записать successor.

Раздельные «предварительная проверка» и последующая безусловная запись не обеспечивают защиту от гонки. При устаревшем precondition сервер возвращает конфликт, а клиент перечитывает объект и заново принимает решение.

`scope_id` и `owner_scope` нельзя менять одновременно с lifecycle-переходом. Перенос между областями управления является отдельным Desired State изменением и не должен маскироваться сменой health-состояния.

## 6. Fail-closed правила

Переход отклоняется, если выполняется хотя бы одно условие:

- неизвестно текущее или целевое состояние;
- направление отсутствует в конечном автомате;
- `desired`/`observation` не соответствует направлению;
- отсутствует или устарел precondition;
- `generation` изменена неверно или повторно использована `resource_version`;
- `state_changed_at` не продвигается либо не совпадает с временем successor;
- для `DEGRADED`, `OFFLINE`, `RECOVERING` или `RETIRED` отсутствует безопасный человекочитаемый reason;
- отсутствует обязательное evidence, присутствует неизвестное или повторное evidence;
- вместе с переходом изменяется scope ownership.

Физически недоступный узел не считается автоматически удалённым или retired. Это предотвращает потерю логической service identity и небезопасный failover stateful workload.

## 7. Совместимость с объектами 0.3.x

Существующие enrollment/heartbeat и inventory объекты 0.3.x остаются читаемыми. Адаптер миграции должен создавать новый lifecycle-объект детерминированно, начиная с `DISCOVERED` и поколения `1`, а затем применять только подтверждённые переходы. Он не имеет права выводить `RETIRED`, `MAINTENANCE` или завершённый Drain только по отсутствию heartbeat.

Read-only HTTP-проекция и endpoint проверки плана описаны в `NODE_LIFECYCLE_API_RU.md`. Они не выполняют переход и не изменяют хост. Транзакционное хранение, scope-aware RBAC/Audit, durable Change/Job, получение evidence от реальных подсистем и восстановление после рестарта входят в последующую интеграцию Node Lifecycle Manager.
