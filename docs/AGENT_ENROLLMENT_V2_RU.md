# Agent Enrollment / Inventory Contract v2

Статус: контракт Control Center 0.4 для декларативной регистрации сведений об узле.

## Назначение и граница безопасности

`POST /api/v1/agent/enrollment/normalize` принимает и канонизирует описание Agent/узла, но **не регистрирует узел и не изменяет систему**. Endpoint не применяет IP/VLAN, не включает forwarding, routing, NAT/port-forwarding и не меняет firewall. Даже сочетание WAN+LAN или заявленная роль `edge-gateway` остаётся только наблюдаемыми/запрашиваемыми данными.

Bootstrap token, приватные ключи, пароли, произвольные команды и network desired state этим контрактом не принимаются. Они не должны помещаться в inventory payload.

Успешный HTTP-ответ означает только структурную корректность данных. Будущий mutating enrollment flow обязан отдельно проверить `preconditions.ready == true`, выполнить аутентификацию bootstrap/mTLS, авторизацию и policy evaluation, а затем пройти стандартный Change/Job pipeline.

Заявленные Agent роли, Site и Management Zone не являются источником полномочий. Окончательное назначение принадлежит Controller policy и RBAC.

## Совместимость

- отсутствие `contract_version` или значение `agent.enrollment/v1` включает совместимый контракт 0.3.x: `node_id`, `hostname`, `capabilities`;
- расширенные поля запрещены в v1, чтобы частичный v2 payload не был ошибочно принят как старый объект;
- v1 normalization всегда возвращает `preconditions.ready=false` и не может считаться admission;
- полный контракт использует явное `contract_version: agent.enrollment/v2`.

Переходный in-memory endpoint `/api/v1/agent/enrollments` сохраняет только v1 и отклоняет v2 целиком. Это исключает частичную запись rich inventory. Поддержка mutating v2 включается только вместе с полной PostgreSQL migration, Audit и Change/Job admission path.

## Поля v2

| Группа | Содержание | Основные ограничения |
|---|---|---|
| Узел | `node_id`, `hostname`, `capabilities` | ограниченные идентификаторы; hostname — корректное DNS-имя |
| Топология | `roles`, `site_id`, `management_zone_id` | только канонические Core-роли; одна физическая машина может иметь несколько ролей |
| Снимок | `collected_at` | единая контрольная точка времени для preconditions |
| Hardware | стабильный machine/UUID/serial identity, architecture, CPU, RAM, storage | CPU topology согласована; размеры положительные; storage ID уникальны |
| Network | до 64 интерфейсов, kind/state/zone, MAC, CIDR, MTU, speed, VLAN parent | только известные zones; VLAN 1..4094; parent существует; циклы запрещены |
| Identity | Agent/installation ID, trusted CA fingerprint, certificate metadata | SHA-256 fingerprints, корректное окно сертификата и ограниченный список DNS names |
| Capacity | типизированные observations с metric/target/value/unit/time/evidence | до 256; metric↔unit фиксированы; target обязан существовать в снимке; значения ограничены |

Канонические network zones: `unassigned`, `wan`, `lan`, `management`, `dmz`, `cluster`, `storage`, `backup`, `trusted`.

Agent v2 использует те же закрытые enum Network Zone, Interface Kind и Link State, что и персистентный Distributed Core. Enrollment payload остаётся наблюдаемым входным снимком; создание `network-zone`/`network-interface` выполняется отдельно через валидируемый `core.object.apply` и не активирует сеть.

Capacity evidence: `measured`, `estimated`, `benchmark`. Неизмеренные значения могут храниться как данные контракта, но сами по себе не подтверждают readiness или сертифицированную capacity.

## Fail-closed preconditions

Ответ содержит стабильные проверки:

- используется v2;
- присутствует базовая роль `agent`;
- Agent заявил capability `inventory`;
- заявленное окно действия сертификата включает `collected_at`;
- наблюдается поднятый интерфейс management/LAN/trusted с IP/CIDR (отдельный Management NIC не обязателен);
- присутствуют свежие измеренные CPU, RAM, storage и network observations;
- для роли Data Node дополнительно присутствует измерение DB latency;
- все observations не старше 15 минут относительно `collected_at`;
- network mutation на этом endpoint отключена.

Если хотя бы одна обязательная проверка не выполнена, `ready=false`. Проверка времени детерминирована относительно `collected_at`; реальный admission обязан повторно проверить фактический mTLS certificate и freshness на стороне Controller.

## Ограничения входа

- Content-Type строго `application/json` (параметр `charset` допустим);
- тело запроса не более 256 KiB;
- неизвестные поля, повторяющиеся JSON keys и trailing JSON запрещены;
- списки имеют жёсткие максимумы, а enum/metric/unit принимают только известные значения;
- некорректная ссылка capacity/VLAN на отсутствующий объект отклоняет весь payload, а не удаляется молча.

Полная машиночитаемая схема находится в `api/openapi-domain-inventory-agent.yaml`.

## Начало этапа 0.5: одноразовый bootstrap token

Внутренний контракт `agent.bootstrap-token/v1` создаёт короткоживущий token,
привязанный к конкретным `node_id`, `scope_id` и transport (`ssh`, `winrm` или
`offline-bundle`). Секрет выдаётся вызывающему коду один раз, хранится только в
виде SHA-256 digest, сравнивается constant-time и атомарно помечается
использованным. Срок действия ограничен диапазоном от одной до пятнадцати минут.

Успешное потребление token возвращает только `BootstrapGrant`: оно не сохраняет
Agent enrollment, не назначает роли и не меняет сеть. Mutating admission обязан
передать grant в PostgreSQL-backed Change/Job/Audit pipeline и повторно
проверить фактический mTLS identity и freshness.
