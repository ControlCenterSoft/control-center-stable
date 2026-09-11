# Control Center 0.30.0 Stable

Статус: **Public Stable**.

0.30.0 добавляет безопасные read-only представления Sites / Nodes / Inventory в Product Web Shell.

## Что нового

- versioned inventory read-model и детерминированная проекция Sites/Nodes;
- аппаратная сводка, роли, capabilities и состояние сетевых интерфейсов;
- явные состояния актуальности `current`, `stale`, `expired` и fail-closed `unavailable`;
- Desired State, Actual State и version skew не подменяются вымышленными данными при отсутствии authoritative evidence;
- адаптивный русскоязычный интерфейс; на узком экране одна карточка узла располагается в строке;
- минимизация чувствительных inventory-полей в обзорном интерфейсе.

## Информационная безопасность

- Unknown/unavailable, stale и expired не преобразуются в Healthy или Success;
- endpoint остаётся read-only и не получает execution authority;
- anonymous и unbound identities не получают доступ;
- site-scoped delegated viewer не получает глобальный inventory aggregate;
- внутренние ошибки источников не раскрываются клиенту;
- ответы не кешируются и используют защиту `nosniff`;
- Identity/RBAC, Session Security и Audit boundaries не ослабляются.

## Совместимость, upgrade и recovery

0.30.0 не добавляет SQL migration и не изменяет опубликованные migrations. Независимая Stable qualification подтверждает clean-install и supported-upgrade на PostgreSQL 15–18, adapter/restart recovery, race/static/unit/build проверки, public-source safety и воспроизводимую Linux AMD64 упаковку.

Перед обновлением обязательны backup/recovery prerequisites и post-upgrade health/readiness проверки. Пользовательские данные, настройки и пароль администратора при поддерживаемом обновлении сохраняются.

## Коммерческая и лицензионная граница

Scope 0.30.0 не добавляет сторонние runtime-компоненты и не меняет dependency graph. Новых обязательств по redistribution, source-offer или NOTICE из-за этого выпуска не возникает. Существующие license/SPDX, distribution/compliance и release-provenance требования сохраняются.
