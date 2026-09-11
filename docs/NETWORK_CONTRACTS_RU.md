# Network Zone и Network Interface — контракт 0.4

Статус: **персистентная модель данных Distributed Core без сетевого runtime**.

## Граница безопасности

`network-zone` и `network-interface` хранят только классификацию, привязки и наблюдаемый inventory. Контракт не содержит команд, provider hooks или полей для route, gateway, IP forwarding, NAT, port forwarding, firewall либо изменения конфигурации хоста. Неизвестные поля отклоняются строгим JSON decode.

Network Zone не является Management Zone:

- `management-zone` задаёт logical policy/RBAC/operational boundary;
- `network-zone` классифицирует сеть внутри Site как `unassigned`, `wan`, `lan`, `management`, `dmz`, `cluster`, `storage`, `backup` или `trusted`;
- наличие WAN и LAN записей не включает передачу пакетов между ними.

## Ссылочная целостность

Оба типа используют общий distributed envelope: `object_id`, `scope_id`, `owner_scope`, `generation`, `resource_version`, `created_at`, `updated_at`.

- Network Zone обязана ссылаться на существующий Site, а её scope должен находиться внутри scope этого Site.
- Network Interface обязана ссылаться на существующие Site и Network Zone; zone, site и scope должны образовывать одну иерархию.
- `node_id` считается известным в Site только при наличии валидного `role-assignment` этого узла в том же Site. Для обычного enrolled узла достаточно канонического назначения роли `agent`.
- Parent Interface обязан существовать на том же node/site/scope. Циклы запрещены.
- VLAN требует parent и `vlan_id` в диапазоне 1..4094; одинаковая пара parent/VLAN на одном узле запрещена.
- На одном узле допускается до 64 интерфейсов; имя интерфейса на узле уникально без учёта регистра.

Физический интерфейс требует канонический 48-bit MAC. MTU ограничен 68..65535. Адреса принимаются только как канонические IPv4/IPv6 CIDR; список содержит до 64 уникальных элементов. `operational_state` — закрытый наблюдаемый enum `up/down/unknown`, а не desired state.

## Persistence и API

Migration `0007_network_contract_objects` только расширяет закрытый PostgreSQL `cc_core_objects.object_type` constraint. Legacy 0.3 таблицы и существующие объекты не переписываются. Down migration отказывается выполняться, пока новые объекты существуют, чтобы не потерять данные молча.

Чтение выполняется через `GET /api/v1/core/objects`, `GET /api/v1/core/objects/{objectId}` и coherent `GET /api/v1/core/topology`. Create и CAS replace используют существующий high-risk `core.object.apply` через Change/Job. Прямого write endpoint и delete до появления Recycle Bin semantics нет.
