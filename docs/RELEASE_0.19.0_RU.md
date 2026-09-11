# Control Center 0.19.0

Статус: кандидат на qualification, не официальный релиз.

Версия 0.19.0 продолжает advisory-only Capacity Planner и расширяет объяснимость Placement Advice: вместо одного итогового bottleneck теперь доступен детерминированный вектор resource headroom по всем квалифицированным ограничениям каждого кандидата.

## Placement Resource Headroom

Новый `internal/capacity/placement_resource_headroom.go` формирует `capacity.placement-resource-headroom/v1` из точного набора входных derivation evidence, исходного `PlacementRequest` и времени оценки.

Для каждого placement-кандидата результат содержит:

- полный отсортированный набор квалифицированных resource constraints;
- observed value, safe/technical limit и абсолютный/процентный reserve;
- тип evidence, время наблюдения и возраст наблюдения;
- limiting constraint, metric и target;
- workload margin и effective safety margin;
- состояние `blocked`, `boundary` или `headroom`;
- confidence исходного placement advice.

Перед построением envelope исходный derived placement snapshot пересчитывается из текущих profile/telemetry inputs. Несовпадение, устаревшие или изменённые evidence отклоняются fail-closed. Итоговый envelope имеет детерминированный fingerprint и отдельную проверку целостности.

JSON contract расположен в `api/capacity-placement-resource-headroom-v1.schema.json`, закрыт для неизвестных полей и фиксирует допустимые metric/unit/evidence/score-band значения.

## Границы безопасности

Placement Resource Headroom является только объясняющим evidence:

- `advisory_only = true`;
- `placement_authorized = false`;
- `production_mutation = false`;
- не изменяет Desired State или Actual State;
- не выполняет placement, drain, migration, resize или rebalance;
- не запускает provider-команды и не изменяет сеть, storage или workload policy.

Добавлены тесты полноты и детерминированного порядка resource vector, выбора limiting constraint, boundary/overload, tamper detection, stale derived evidence и свойства, что effective safety margin не превышает входные ограничения.

Официальный выпуск 0.19.0 допускается только после отдельной qualification и release gates.
