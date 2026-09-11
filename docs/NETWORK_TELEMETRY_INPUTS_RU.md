# Network Telemetry Inputs — контракт Control Center 0.6

Статус: типизированный read-only вход для Capacity Planner без сетевого runtime и без мутаций.

## Граница контракта

Пакет `internal/networktelemetry` принимает только числовые наблюдения, уже отнесённые к логическому уровню `interface`, `zone` или `site`. Идентификаторы непрозрачны. Контракт не переносит IP- или MAC-адреса, credentials, provider payload, endpoints, маршруты, шлюзы, правила firewall/NAT, связи топологии, desired state или команды.

Поддерживаются закрытые метрики:

- receive/transmit throughput в `bits-per-second`;
- receive/transmit utilization в `percent`;
- receive/transmit errors в `errors-per-second`;
- latency в `milliseconds`;
- packet loss в `percent`.

Неизвестные JSON-поля и дополнительные документы отклоняются. Вход ограничен 2 MiB, 1024 наблюдениями и окнами не длиннее 24 часов.

## Окна, freshness и confidence

Каждое наблюдение содержит точные начало/конец окна и число samples. Окна одной серии `target + metric` не могут пересекаться: это исключает двойной учёт. Время окончания сравнивается с переданным вызывающей стороной доверенным `evaluatedAt`; будущее или превышение `max_age_seconds` отклоняет весь batch.

Confidence имеет строгие непересекающиеся диапазоны:

| Level | Score |
|---|---:|
| `low` | `[0, 0.5)` |
| `medium` | `[0.5, 0.8)` |
| `high` | `[0.8, 1]` |

При агрегации сохраняется минимальный confidence серии. Freshness округляется вверх до целой секунды, чтобы не занижать возраст данных.

## Агрегация и bottleneck

Для каждой серии детерминированно вычисляются latest value, взвешенное по числу samples среднее, peak, суммарное число samples и общий диапазон окон. При одинаковом latest timestamp выбирается лексикографически меньший observation ID.

Capacity boundary содержит только `safe_limit` и более высокий `technical_limit` для одной серии. `BuildRecommendationInput` требует свежий aggregate для каждой boundary и выбирает bottleneck с минимальным `safe_reserve_percent`; равенство разрешается по boundary ID.

Результат всегда имеет `advisory_only: true` и `production_mutation: false`. Он является входом расчёта рекомендации, а не планом либо разрешением на изменение сети.
