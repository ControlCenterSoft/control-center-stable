# Control Center 0.21.0

Статус: кандидат на qualification, не официальный релиз.

Версия 0.21.0 продолжает advisory-only Capacity Planner и добавляет единый детерминированный gate повторного использования placement advice, связывающий strong-evidence revalidation с freshness каждого resource constraint.

## Placement Resource Reuse Gate

Новый `internal/capacity/placement_resource_reuse_gate.go` формирует контракт `capacity.placement-resource-reuse-gate/v1`.

Gate принимает exact reuse decision, sealed multi-resource headroom envelope и exact resource freshness gate. Перед verdict заново проверяются strong-evidence consumption, ресурсная freshness и lineage между decision, derived snapshot, placement snapshot, advice и headroom envelope.

Повторное использование advice разрешается только когда одновременно выполнены оба независимых условия: strong-evidence reuse остаётся разрешённым и все resource constraints имеют актуальное измеренное evidence. Любой drift lineage, stale/degraded resource evidence или блокирующий strong-evidence verdict приводит к fail-closed результату с конкретной причиной и recommended action.

JSON contract расположен в `api/capacity-placement-resource-reuse-gate-v1.schema.json`, закрыт для неизвестных полей и фиксирует взаимосогласованные allowed/blocked состояния.

## Границы безопасности

Reuse Gate остаётся только advisory/evidence-контролем:

- `advisory_only = true`;
- `placement_authorized = false`;
- `production_mutation = false`;
- не изменяет Desired State или Actual State;
- не выполняет placement, drain, migration, resize или rebalance;
- не запускает provider-команды и не изменяет сеть, storage или workload policy.

Добавлены тесты allowed/blocked verdict, resource-staleness, strong-evidence block, lineage drift, tamper detection и JSON-contract semantics.

Официальный выпуск 0.21.0 допускается только после отдельной qualification и release gates.
