# Control Center 0.16.0

Версия 0.16 продолжает bounded nonlinear Workload Curve 0.15 и добавляет отдельный анализ предельной эффективности масштабирования внутри уже измеренного диапазона. Цель — обнаруживать diminishing returns до рекомендации наращивания ресурсов и не трактовать рост resource factor как гарантированно пропорциональный рост безопасной нагрузки.

## Workload Curve Efficiency

`internal/capacity/workload_curve_efficiency.go` принимает только exact `capacity.workload-curve/v1` evidence и policy с минимально допустимым относительным коэффициентом эффективности и минимальным confidence.

Перед анализом исходная кривая полностью пересобирается через `BuildWorkloadCurve`. Несовпадение `curve_id`, status, reason или confidence отклоняется как tampered/stale evidence.

Для каждого измеренного сегмента рассчитываются:

- прирост safe workload на единицу resource factor;
- относительная эффективность по сравнению с первым измеренным положительным сегментом;
- минимальная эффективность среди всех наблюдаемых сегментов.

Если относительная эффективность любого последующего сегмента падает ниже заданной policy, результат получает состояние `diminishing-returns` и рекомендацию сначала исследовать bottleneck, а не автоматически увеличивать ресурсы. Недостаточный confidence приводит к `collect-evidence`. Непригодная исходная кривая или отсутствие положительного базового прироста блокируют отчёт fail-closed.

Анализ дополнительно отклоняет malformed ready-curve evidence с недостаточным числом точек, не возрастающие resource-factor segments и нечисловые/бесконечные производные значения. Для каждого сегмента границы safe workload привязаны к соответствующим левой и правой измеренным точкам.

## Границы безопасности

Efficiency Report является только аналитическим evidence:

- `advisory_only = true`;
- `production_mutation = false`;
- не выполняет resize, placement, drain, migration или rebalance;
- не меняет Desired State, Actual State, сеть, storage или workload policy;
- не запускает provider-команды;
- не экстраполирует capacity за пределы измеренной кривой;
- не создаёт автоматического разрешения на масштабирование или закупку оборудования.

Добавлены проверки deterministic report identity, выявления diminishing returns, достаточной эффективности, fail-closed confidence policy, malformed/non-increasing evidence и tamper detection exact curve evidence. JSON contract закрыт для неизвестных полей и фиксирует non-mutation boundary.
