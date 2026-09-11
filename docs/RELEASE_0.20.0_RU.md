# Control Center 0.20.0

Статус: кандидат на qualification, не официальный релиз.

Версия 0.20.0 продолжает advisory-only Capacity Planner и добавляет детерминированный freshness gate для Placement Resource Headroom. Gate проверяет, можно ли повторно использовать ранее рассчитанный resource-headroom envelope на основании точного исходного evidence и индивидуального freshness budget каждого ограничения.

## Placement Resource Headroom Freshness Gate

Новый `internal/capacity/placement_resource_headroom_freshness.go` формирует контракт `capacity.placement-resource-headroom-freshness/v1`.

Перед выдачей verdict исходный headroom envelope повторно валидируется на его точном времени оценки. Затем для каждого кандидата проверяются все resource constraints относительно `checked_at`: возраст наблюдения, `max_observation_age_seconds`, тип evidence и confidence. Несовпадение lineage, подмена envelope или отсутствие derivation input отклоняются fail-closed.

Gate различает `current`, `degraded-evidence` и `stale`. Повторное использование advice разрешается только когда все ограничения остаются текущими, измеренными и достаточной уверенности. Для устаревших или деградированных данных возвращается конкретное действие по сбору свежей telemetry/evidence.

JSON contract расположен в `api/capacity-placement-resource-headroom-freshness-v1.schema.json`, закрыт для неизвестных полей и фиксирует допустимые статусы, причины и recommended actions.

## Границы безопасности

Freshness Gate является только evidence-контролем:

- `advisory_only = true`;
- `placement_authorized = false`;
- `production_mutation = false`;
- не изменяет Desired State или Actual State;
- не выполняет placement, drain, migration, resize или rebalance;
- не запускает provider-команды и не изменяет сеть, storage или workload policy.

Добавлены тесты current/stale/degraded evidence, детерминированности verdict, проверок lineage и tamper detection.

Официальный выпуск 0.20.0 допускается только после отдельной qualification и release gates.
