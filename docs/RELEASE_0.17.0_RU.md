# Control Center 0.17.0

Статус: активная ветка разработки, не официальный релиз.

Версия 0.17 продолжает nonlinear Workload Curve 0.15 и Efficiency Report 0.16, добавляя bounded scale-scenario evaluation. Новый контур отвечает на вопрос, помещается ли заданная плановая нагрузка с требуемым headroom на конкретном resource factor, но только внутри реально измеренной benchmark-кривой.

## Workload Scale Scenario

`internal/capacity/workload_scale_scenario.go` принимает exact Workload Curve, exact Efficiency Report и запрос с target resource factor, required workload и минимальным headroom.

Перед расчётом Efficiency Report полностью пересобирается из исходной кривой и policy. Любое изменение report fields, ID, status или reason отклоняется как evidence mismatch.

Сценарий:

- использует `EstimateWorkloadCurve()` и поэтому не выполняет extrapolation за пределами измеренного диапазона;
- рассчитывает доступную safe workload и фактический headroom;
- возвращает `insufficient-capacity`, если требуемый резерв не выдерживается;
- сохраняет предупреждение `diminishing-returns`, даже когда абсолютная ёмкость достаточна;
- возвращает `collect-evidence` при недостаточном или вне диапазона evidence;
- fail-closed блокирует неподтверждённые входные данные.

## Сравнение вариантов масштабирования

`internal/capacity/workload_scale_options.go` сравнивает до 64 bounded вариантов resource factor для одного и того же `required_workload` и `minimum_headroom_percent`.

Контур:

- детерминированно сортирует варианты и отклоняет дублирующиеся resource factor;
- для каждого варианта повторно вызывает exact `EvaluateWorkloadScaleScenario`, поэтому не принимает caller-supplied оценку ёмкости как доказательство;
- выбирает минимальный evidence-backed resource factor, который сохраняет требуемый headroom;
- не скрывает `diminishing-returns`: такой результат остаётся отдельным статусом с рекомендацией проверить bottleneck до масштабирования;
- если измеренная кривая не покрывает безопасный вариант, возвращает `collect-evidence`, а не extrapolation;
- если все измеренные варианты недостаточны, возвращает `insufficient-capacity` без автоматического изменения ресурсов.

Результат имеет стабильный `wso-*` идентификатор и закрытый JSON contract `api/capacity-workload-scale-options-v1.schema.json`.

## Границы безопасности

Scale Scenario и Scale Options остаются аналитическим evidence:

- `advisory_only = true`;
- `production_mutation = false`;
- не выполняют resize, placement, drain, migration или rebalance;
- не меняют Desired State или Actual State;
- не изменяют сеть, storage или workload policy;
- не запускают provider-команды;
- не создают разрешение на закупку или автоматическое масштабирование.

Добавлены проверки безопасного сценария, недостаточного headroom, сохранения diminishing-returns warning, запрета extrapolation и tamper detection для exact Efficiency Report. Для Scale Options дополнительно проверяются детерминированность независимо от порядка входа, выбор минимального безопасного resource factor, отсутствие выбора при collect-evidence, недостаточная ёмкость, duplicate factor и несовместимые demand targets. JSON contracts закрыты для неизвестных полей.

Официальный выпуск 0.17.0 допускается только после отдельной qualification и release gates.
