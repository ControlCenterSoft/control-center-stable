# Control Center 0.15.0

Версия 0.15 продолжает benchmark-backed Workload Profile 0.14 и добавляет
bounded nonlinear capacity curve. Цель — учитывать, что рост безопасной
производительности не обязан быть линейным относительно роста ресурсов, но не
выдумывать ёмкость за пределами реально измеренного диапазона.

## Workload Curve

`internal/capacity/workload_curve.go` принимает от 3 до 64 точек, каждая из
которых связывает конкретный `capacity.workload-profile/v1` evidence с
нормализированным resource factor и безопасной workload boundary.

Точки канонизируются по resource factor, а идентификаторы точек, исходных
профилей и scale factors обязаны быть уникальными. Curve ID детерминирован и
привязан к exact набору evidence, scope, workload unit и минимальному числу
требуемых точек.

Если при увеличении resource factor наблюдаемая safe workload уменьшается,
кривая считается противоречивой и получает состояние `blocked`. Confidence
кривой выбирается консервативно как минимальный confidence среди входных точек.

## Piecewise interpolation без extrapolation

`EstimateWorkloadCurve()` сначала полностью пересобирает кривую из сохранённых
точек и отклоняет любое несовпадение exact `curve_id`, status, reason или
confidence. Это защищает от использования изменённого или частично
перепривязанного evidence.

Внутри наблюдаемого диапазона используется piecewise-linear interpolation,
поэтому разные сегменты могут иметь разный slope. Точная измеренная точка
возвращается без интерполяции.

За пределами минимального/максимального измеренного resource factor система не
экстраполирует capacity. Вместо предположительной цифры возвращается
`collect-evidence` с причиной `target_outside_observed_curve`.

## Границы безопасности

Workload Curve и Estimate являются только аналитическим evidence:

- `advisory_only = true`;
- `production_mutation = false`;
- не меняют Capacity Profile, Desired State или Actual State;
- не выполняют placement, drain, migration, resize или rebalance;
- не запускают provider-команды;
- не меняют сеть, storage или workload policy;
- не инициируют закупку или масштабирование;
- не используют extrapolation для неподтверждённого масштаба.

Добавлены проверки детерминированности, консервативного confidence,
интерполяции внутри измеренного диапазона, запрета extrapolation,
блокировки противоречивой кривой, duplicate resource factor и tamper detection
для exact curve evidence.

JSON contracts закрыты к неизвестным полям и фиксируют non-mutation boundary.
