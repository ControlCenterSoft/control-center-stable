# Edge Gateway: явная авторизация сетевых планов

Контракт Control Center 0.6 разрешает сформировать план `routing`, `nat` или `port-forwarding` только для узла, которому явно назначена роль `edge-gateway`. Наличие двух сетевых интерфейсов, сочетание WAN+LAN либо сама роль ничего автоматически не включают.

Реализация находится в отдельном side-effect-free пакете `internal/edgegateway`; схема опубликована в `api/openapi-edge-gateway.yaml`. Контракт не регистрирует HTTP endpoint, desired state или worker action и не меняет настройки узла.

## Обязательные условия

Запрос обязан одновременно содержать:

1. точную версию схемы и режим `plan`;
2. одну явно выбранную операцию без `auto`/автоопределения;
3. узел, Site и Scope;
4. разные WAN- и LAN-интерфейсы с разными физическими путями;
5. зоны этих интерфейсов, классифицированные ровно как `wan` и `lan` в том же Site/Scope;
6. сохранённое назначение роли `edge-gateway` тому же узлу в том же Site/Scope;
7. ссылку на явное решение policy engine со статусом `approved`;
8. точную capability выбранной операции.

| Операция | Обязательная capability |
|---|---|
| `routing` | `network.edge.routing.plan` |
| `nat` | `network.edge.nat.plan` |
| `port-forwarding` | `network.edge.port-forwarding.plan` |

Инвентарь интерфейсов и зон рассматривается как coherent snapshot. Дубликаты, отсутствующие родители, циклы и переход parent-связи через границу узла/Site/Scope отклоняются. Два VLAN одного физического пути не считаются независимыми WAN/LAN-интерфейсами.

## Результат и граница безопасности

При успешной проверке формируется детерминированный `network.edge-gateway.authorization-plan/v1` с ссылками на Role Assignment, `resource_version`, интерфейсы, зоны, approval и capability. План всегда содержит:

- `authorized: true` — входные доказательства достаточны только для формирования плана;
- `production_mutation_enabled: false` — применение в production отключено;
- закрытый список проверочных шагов без command, endpoint, credentials, secrets и произвольного исполняемого payload.

Отдельной операции исполнения нет. Для будущего применения потребуется отдельный change/approval/job/verify контур, который не входит в этот контракт.

## Стабильные причины отказа

Клиент должен использовать поле `code`, а не текст пояснения.

| Код | Причина |
|---|---|
| `EDGE_REQUEST_INVALID` | Нарушена структура либо версия запроса |
| `EDGE_OPERATION_EXPLICIT_REQUIRED` | Операция не выбрана или запрошен `auto` |
| `EDGE_OPERATION_UNSUPPORTED` | Операция не входит в закрытый список |
| `EDGE_PLAN_ONLY` | Запрошен режим, отличный от `plan` |
| `EDGE_ROLE_REQUIRED` | Нет корректного явного Role Assignment |
| `EDGE_APPROVAL_REQUIRED` | Нет явного одобренного policy decision |
| `EDGE_CAPABILITY_REQUIRED` | Нет точной capability операции |
| `EDGE_DISTINCT_INTERFACES_REQUIRED` | WAN/LAN совпадают или используют один физический путь |
| `EDGE_INTERFACE_NOT_FOUND` | Указанный интерфейс отсутствует |
| `EDGE_INTERFACE_BOUNDARY_MISMATCH` | Интерфейс принадлежит другому узлу/Site/Scope |
| `EDGE_ZONE_NOT_FOUND` | Зона интерфейса отсутствует |
| `EDGE_ZONE_BOUNDARY_MISMATCH` | Зона принадлежит другому Site/Scope |
| `EDGE_WAN_ZONE_REQUIRED` | WAN-интерфейс не относится к зоне `wan` |
| `EDGE_LAN_ZONE_REQUIRED` | LAN-интерфейс не относится к зоне `lan` |
| `EDGE_INVENTORY_INVALID` | Snapshot неоднозначен либо нарушает parent-граф |
| `EDGE_NETWORK_SAFETY_POLICY` | Базовая network safety policy отказала |

JSON decoder ограничен 16 KiB, принимает ровно один объект и отклоняет неизвестные поля. Поэтому скрытая директива исполнения не может быть молча проигнорирована.
