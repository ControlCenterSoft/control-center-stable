# Control Center 0.29.0 Stable

Статус: **Public Stable**.

0.29.0 добавляет Product Web Shell / Overview v2 — основу нового Web UI с контекстами «Установка / Сайт / Узел», явными признаками среды, актуальности, риска и состояния, базовой локалью `ru-RU`, адаптивной компоновкой и accessibility markers.

## Безопасность

- Unknown, Stale, Degraded и не загруженное состояние не подменяются Healthy;
- новый shell сам по себе не предоставляет execution authority и production mutation;
- identity-derived значения выводятся с HTML escaping;
- критический статус не кодируется только цветом;
- Identity/RBAC, Session Security и Audit boundaries не ослабляются;
- пользовательские credentials и пароль администратора при обновлении сохраняются.

## Совместимость и recovery

0.29.0 не добавляет SQL migration и не изменяет опубликованные migrations. Независимая Stable qualification подтверждает clean-install и supported-upgrade на PostgreSQL 15–18, adapter/restart recovery, race/static/unit/build проверки, public-source safety и воспроизводимую Linux AMD64 упаковку.

Перед обновлением обязательны backup/recovery prerequisites и post-upgrade health/readiness проверки.

## Коммерческая и лицензионная граница

Scope 0.29.0 не добавляет сторонние runtime-компоненты и не меняет dependency graph. Новых обязательств по redistribution, source-offer или NOTICE из-за этого выпуска не возникает. Существующие license/SPDX, distribution/compliance и release-provenance требования сохраняются.
