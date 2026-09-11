# Capacity Contracts 0.4

Статус: безопасный контрактный срез Control Center 0.4 для профиля ёмкости, ограничений и рекомендаций.

Документ дополняет `ARCHITECTURE.md`, `ROADMAP.md`, `docs/REQUIREMENTS_RU.md`, `docs/DISTRIBUTED_CORE_CONTRACTS_RU.md` и `docs/AGENT_ENROLLMENT_V2_RU.md`. Машиночитаемый контракт находится в `api/openapi-capacity-contracts.yaml`.

## Назначение и граница

Пакет `internal/capacity` содержит только ограниченные типы и чистые функции нормализации/валидации. Он отвечает на вопросы «какова безопасная граница», «какой ресурс ограничивает профиль» и «какое действие можно вынести на рассмотрение». Он ничего не сохраняет и не выполняет.

В текущий срез намеренно не входят:

- scheduler и placement execution;
- автоматический перенос ролей или workload;
- изменение сети, storage или конфигурации сервисов;
- заказ/покупка оборудования;
- произвольные команды, URL, credentials или provider payload;
- PostgreSQL persistence, HTTP endpoint, RBAC/Change/Job/Audit wiring;
- прогноз тренда и срок исчерпания резерва.

Любая рекомендация имеет обязательное `advisory_only: true`. Её дальнейшее применение в будущих релизах должно пройти обычный путь `Authorization → Policy → Plan → Change → Job → typed action → Verify → Audit`.

## Контракты

| Контракт | Версия | Назначение |
|---|---|---|
| `Constraint` | `capacity.constraint/v1` | Ограничение одной канонической Agent-метрики для конкретного target |
| `CapacityProfile` | `capacity.profile/v1` | Калиброванный профиль безопасной и технической ёмкости субъекта |
| `Recommendation` | `capacity.recommendation/v1` | Детерминированная advisory-оценка текущей нагрузки и bottleneck |

`CapacityProfile` и `Recommendation` используют общий distributed envelope: `object_id`, `scope_id`, `owner_scope`, `generation`, `resource_version`, `created_at`, `updated_at`. Проверки successor соблюдают каноническую семантику поколения и непрозрачной версии ресурса.

Recommendation обязательно содержит precondition на `object_id + resource_version + generation` профиля. Результат нельзя корректно построить по устаревшему или другому профилю.

## Единый словарь наблюдений

Capacity Planner не объявляет собственные metric/unit/evidence enums. `Evidence` встраивает `agent.CapacityObservation`, а `Constraint` проверяется по тому же Agent registry:

- единица измерения однозначно определяется метрикой;
- target kind берётся из определения метрики (`node`, `storage`, `network-interface`, `database`, `service`);
- значения конечны, неотрицательны и имеют общий защитный верхний предел;
- evidence принимает только `measured`, `estimated` или `benchmark`.

Добавление метрики требует одного изменения в Agent registry и тесте его полноты. Параллельный capacity-словарь запрещён.

Generic Capacity-контракт проверяет канонический формат `target_id`, metric↔unit и совпадение target между Constraint и Evidence. Принадлежность storage/interface конкретному inventory snapshot проверяет Agent при формировании исходного observation; Capacity Planner не выдумывает отсутствующий inventory context и не ослабляет эту upstream-проверку.

## Safe capacity и technical limit

`technical_limit` — предел насыщения/техническая верхняя граница. Это не обещание надёжной работы. `safe_capacity` — более низкая эксплуатационная граница, сохраняющая заданный резерв.

Для workload:

`safe_reserve = safe_capacity - current_workload`

`technical_reserve = technical_limit - current_workload`

Для отдельного Constraint те же разности вычисляются в единице его метрики. Дополнительно вычисляется процент:

`safe_reserve_percent = (safe_limit - observed_value) / safe_limit × 100`

Отрицательный reserve означает превышение соответствующей границы. Profile отклоняется, если `safe_capacity >= technical_limit` или фактический процент между ними меньше `minimum_reserve_percent`.

`minimum_reserve_percent` — только нижняя контрактная граница. Производитель профиля обязан уже включить в `safe_capacity` эксплуатационные пики и требуемый failure reserve; контракт не превращает technical limit в безопасное значение и не обещает работоспособность при отказе узла без подтверждающей evidence.

## Evidence и confidence

Каждый Constraint задаёт:

- допустимые evidence kinds;
- минимальное суммарное число samples;
- максимальный возраст observation (от 1 секунды до 7 суток).

При калибровке возраст считается относительно `calibrated_at`. При рекомендации — относительно `evaluated_at`. Observation из будущего, устаревшее наблюдение, неизвестный target/metric/unit, недостаточное число samples или непокрытый Constraint отклоняют объект целиком.

Confidence имеет строгие диапазоны:

| Уровень | Score | Дополнительное условие |
|---|---:|---|
| `low` | `[0, 0.5)` | допустим только совет `collect-evidence` |
| `medium` | `[0.5, 0.75)` | требуется хотя бы одно не-estimated evidence |
| `high` | `[0.75, 0.95)` | требуется хотя бы одно не-estimated evidence |
| `certified` | `[0.95, 1]` | measured + benchmark и не менее 100 samples |

Confidence рекомендации не может быть выше confidence профиля, на котором она построена.

## Детерминированный bottleneck

Для каждого Constraint выбирается самое новое допустимое свежее observation. При одинаковом времени побеждает лексикографически меньший `evidence_id`. Bottleneck — Constraint с наименьшим `safe_reserve_percent`; при равенстве побеждает лексикографически меньший `constraint_id`.

Все derived fields рекомендации (`subject`, workload unit, safe/technical capacity, оба workload reserve и весь bottleneck) пересчитываются из защищённого профиля и evidence. Переданное клиентом несовпадающее значение считается подменой и отклоняется.

Условные инварианты, которые выражаются JSON Schema 2020-12, также закреплены в OpenAPI: role обязателен только для role-subject, диапазон score зависит от confidence level, role-actions требуют `target_role`, а low confidence допускает только `collect-evidence`. Остальные межполевые проверки выполняет тот же Go validator.

Доступные advisory actions ограничены enum. `increase-storage` разрешён только при storage bottleneck, `increase-network` — только при network bottleneck. Role actions требуют каноническую Core role. Если safe boundary не нарушена, операционный action запрещён; допустимы только `none` или `collect-evidence`. Если граница нарушена, `none` запрещён.

## Совместимость и fail-closed migration

До этого среза persisted `CapacityProfile`, `Constraint` или `Recommendation` не существовали. Поэтому не существует корректного legacy payload, который можно автоматически преобразовать в v1 без выдумывания safe limit, evidence или confidence.

Правило совместимости детерминировано:

- принимается только точная явная версия `*/v1`;
- отсутствующая, неизвестная или будущая версия отклоняется `ErrUnsupportedSchema`;
- частичный объект не дополняется опасными значениями по умолчанию;
- списки Constraints/Evidence имеют жёсткие пределы и каноническую сортировку;
- дубликаты ID, metric/target и observation signature отклоняются.

Будущий persisted v1 должен получить отдельную PostgreSQL migration и транзакционный CAS/API слой. До этого OpenAPI-фрагмент намеренно объявляет `paths: {}` и не создаёт ложного mutating API.

## Следующие интеграционные шаги

Для полного exit gate 0.4 ещё нужны PostgreSQL schema/upgrade path, authenticated RBAC API, Audit и связь с Change/Job без исполнения рекомендаций. Runtime Capacity Planner, synthetic load calibration, placement planner и действия по рекомендациям относятся к 0.5 и более поздним этапам `ROADMAP.md`.
