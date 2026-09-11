# Control Center 0.18.0

Статус: кандидат на qualification, не официальный релиз.

Версия 0.18.0 продолжает advisory-only Capacity Planner и добавляет детерминированное объяснение запаса безопасности для каждого кандидата Placement Advice.

## Placement Safety Margin

Новый `internal/capacity/placement_safety_margin.go` строит `capacity.placement-safety-margin/v1` только из текущего `PlacementRequest`, исходных `NodeProjection` и exact `PlacementAdvice`. Перед формированием результата advice полностью пересобирается через `BuildPlacementAdvice`; изменённые eligibility, reserve, confidence или recommendation поля отклоняются fail-closed.

Для каждого кандидата результат показывает:

- запас workload относительно минимального требуемого резерва;
- bottleneck reserve и фактически ограничивающее измерение;
- эффективный safety margin;
- состояние `blocked`, `boundary` или `headroom`;
- confidence, на котором основано решение.

JSON contract находится в `api/capacity-placement-safety-margin-v1.schema.json` и закрыт для неизвестных полей.

## Границы безопасности

Placement Safety Margin является только объясняющим evidence:

- `advisory_only = true`;
- `placement_authorized = false`;
- `production_mutation = false`;
- не меняет Desired State или Actual State;
- не выполняет placement, drain, migration, resize или rebalance;
- не запускает provider-команды и не изменяет сеть, storage или workload policy.

Добавлены тесты детерминированности, boundary/confidence-block, выбора bottleneck как ограничивающего измерения и fail-closed tamper detection для входного advice и выходного envelope.

Официальный выпуск 0.18.0 допускается только после отдельной qualification и release gates.
