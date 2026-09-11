# Control Center 0.10.0

Версия 0.10 продолжает Capacity Planner и добавляет прогнозируемый горизонт
исчерпания безопасной ёмкости. Модель связывает уже рассчитанный workload
forecast с отказоустойчивой fleet assessment и отвечает на вопрос, сколько
времени остаётся до достижения безопасной границы при наблюдаемом тренде.

## Capacity Horizon

`internal/capacity/horizon.go` принимает только уже сформированные
`capacity.forecast/v1` и `capacity.assessment/v1` одного scope и workload unit.
Смешивание данных разных площадок или разных единиц нагрузки отклоняется.

На основании текущей нагрузки, дневного тренда и безопасной ёмкости после
учёта failure reserve рассчитываются:

- текущий безопасный резерв;
- `days_to_safe_capacity` при положительном тренде роста;
- риск `healthy`, `warning`, `critical` или `unknown`;
- рекомендательное действие без выполнения инфраструктурных изменений.

Если нагрузка не растёт и парк остаётся безопасным, конечный срок исчерпания
ёмкости не выдумывается: `days_to_safe_capacity` остаётся пустым. Если
безопасная граница уже достигнута, горизонт равен нулю.

## Fail-closed классификация

Пороговые окна задаются явно и должны удовлетворять правилу
`0 <= critical <= warning <= 3650` дней.

- низкая достоверность forecast или assessment → `unknown` и `collect-evidence`;
- уже небезопасная fleet assessment → `critical` и `add-role-capacity`;
- достижение безопасной границы внутри critical window → `critical`;
- достижение границы внутри warning window → `warning` и `adjust-workload-policy`;
- достаточный временной резерв → `healthy`.

Решение использует безопасную ёмкость после учёта отказоустойчивого резерва,
поэтому прогноз не подменяет HA-требования обычной суммой номинальных ресурсов.

## Границы безопасности

Capacity Horizon остаётся аналитическим evidence:

- `advisory_only = true`;
- `production_mutation = false`;
- не изменяет Desired State или Actual State;
- не выполняет placement, drain, migration или resize;
- не запускает provider-команды;
- не изменяет сеть, хранилище и workload policy автоматически;
- не создаёт закупку оборудования.

Версия 0.10 сохраняет накопленные возможности Capacity Planner: прогноз и
what-if, безопасную fleet assessment, advisory placement и bottleneck-анализ.
