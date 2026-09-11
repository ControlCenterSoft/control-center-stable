# Market Manifest v2

Статус: контракт Control Center 0.4 для описания Market workload.

Machine-readable OpenAPI: [`../api/openapi-market-manifest-v2.yaml`](../api/openapi-market-manifest-v2.yaml).

## Назначение

Manifest v2 описывает возможности модуля до его установки: жизненный цикл, тип workload, проверенную ёмкость, восстановление, сетевые потребности, зависимости, хранилища, секреты и совместимость. Manifest является декларацией, а не командой исполнения.

API чтения:

- `GET /api/v1/market/manifests` и `GET /api/v1/market/manifests/{id}` сохраняют исходный переходный контракт v1;
- `GET /api/v2/market/manifests` и `GET /api/v2/market/manifests/{id}` возвращают Manifest v2;
- оба API защищены тем же разрешением `market.read`;
- endpoints только читают каталог: query-параметры и изменяющие HTTP-методы не поддерживаются.

## Основные инварианты

- `schema_version` всегда равен `market.manifest/v2`;
- ID, capability, provider и другие машинные имена нормализованы и не содержат скрытых пробелов;
- неизвестные JSON-поля отклоняются при строгом декодировании;
- повторяющиеся lifecycle, dependency, zone и capability записи отклоняются;
- `PRODUCTION_READY` разрешён только для полного, согласованного контракта;
- `UNSPECIFIED` допустим только для явно переходного объекта;
- установка модуля не означает его активацию.

## Lifecycle

Поддерживаемые операции:

`install → configure → health → capacity → scale_placement → update → migrate → drain → backup → restore → failover → remove`.

Для `PRODUCTION_READY` обязательны install, configure, health, capacity, update, backup, restore и remove. Stateful/clustered workloads дополнительно обязаны объявить migrate и drain, а clustered workload — failover.

Lifecycle и recovery проверяются совместно: например, `backup` нельзя объявить без `recovery.backup=SUPPORTED`, а поддерживаемый failover обязан иметь lifecycle-операцию `failover`.

## Capacity Profile

Профиль содержит единицу workload, минимальный resource envelope, safe capacity, technical limit, confidence, benchmark scenario и evidence. Resource envelope покрывает CPU, RAM, storage capacity, storage IOPS, network bandwidth, DB connections и service queue depth.

Статусы профиля:

- `UNVERIFIED` — capacity claim отсутствует: safe/technical limits равны нулю, confidence равен `UNKNOWN`, evidence отсутствует;
- `ESTIMATED` — есть воспроизводимый benchmark и evidence, но профиль ещё не сертифицирован;
- `CERTIFIED` — профиль подтверждён установленным процессом испытаний.

Для `ESTIMATED` и `CERTIFIED` safe capacity должна быть положительной и не превышать technical limit. Evidence содержит тип, ссылку и SHA-256 digest. `PRODUCTION_READY` с `UNVERIFIED` запрещён.

## Recovery

Для backup, restore и failover отдельно указывается `UNDECLARED`, `NOT_APPLICABLE` или `SUPPORTED`. Это исключает интерпретацию отсутствующего значения как фактической поддержки.

Production-ready stateful/clustered workload обязан иметь Recovery Adapter и положительные RPO/RTO. Stateful failover требует fencing. Fencing без объявленного failover отклоняется.

## Network и явная активация

`network.enforcement` имеет единственное допустимое значение `POLICY_CONTROLLED`. Manifest может объявить protocol, direction, exposure, zones и port ranges, но эти данные не открывают порты, не создают маршруты, NAT или port forwarding.

`activation.mode=EXPLICIT` означает отдельное действие оператора. `activation.network_changes=POLICY_APPROVAL_REQUIRED` требует проверки Network/Security policy до любого изменения сети. Значения вроде `AUTOMATIC` или `AUTO_APPLY` не входят в контракт и отклоняются.

## Dependencies

Контракт различает обязательные и опциональные module dependencies и задаёт ограничение semver. Самозависимость и повторяющиеся модули запрещены.

Дополнительно объявляются:

- capability dependencies;
- storage class, access mode, minimum size и persistence;
- только имя, назначение и scope необходимого секрета — значение секрета в manifest никогда не помещается.

Dependency metadata описывает preflight для будущего планировщика. Она не устанавливает зависимость автоматически.

## Совместимость v1 и детерминированная миграция

Контракт v1 остаётся доступным без изменения ответа. Встроенные v1 manifests представляются через v2 посредством `MigrateBuiltinManifestV1` по стратегии `PRESERVE_DECLARATIONS`:

- `install` сохраняется как `install`, `upgrade` отображается в `update`, `remove` сохраняется как `remove`;
- ID, версия, capabilities, providers и platforms сохраняются и приводятся к стабильному порядку;
- неизвестный workload не угадывается и становится `UNSPECIFIED`;
- capacity становится `UNVERIFIED` без выдуманных пределов;
- recovery остаётся `UNDECLARED` без фиктивного adapter;
- network requirements и dependencies остаются пустыми;
- activation всегда `EXPLICIT`, network changes всегда требуют policy approval;
- объект получает `TRANSITIONAL` и список предупреждений о данных, которых не было в v1.

Один и тот же вход v1 всегда формирует структурно одинаковый v2. Переходный manifest нельзя считать production-ready, пока разработчик модуля явно не заполнит и не проверит недостающие контракты.

## Проверка

Go API предоставляет:

- `ValidateManifestV2` для семантической проверки типизированного manifest;
- `DecodeManifestV2` для строгого JSON decode с запретом неизвестных полей и дополнительных JSON-значений;
- `MigrateBuiltinManifestV1` для консервативной миграции;
- `BuiltinManifestsV2` и `FindBuiltinManifestV2` для каталога встроенных модулей.

Unit/API/RBAC tests покрывают корректный stateless и clustered contracts, несовместимые lifecycle/recovery комбинации, недоказанные capacity claims, небезопасные network/activation значения, неверные dependencies, строгий JSON decode и повторяемость миграции.
