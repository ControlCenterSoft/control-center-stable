# Control Center — продуктовая дорожная карта

Статус: **CURRENT**. Текущий опубликованный Public Stable — **0.31.0**. Corrective **0.31.1** находится в qualification и не считается Public Stable до завершения release cycle и отдельной публикации.

Опубликованная основа включает Identity/RBAC/Audit, Site/Network foundation, Capacity Intelligence foundation, Session Security, recovery/network evidence freshness, Product Web Shell, Sites / Nodes / Inventory и безопасный Changes / Jobs operational workflow с approval, durable Job, verification и recovery evidence.

## Ближайший обязательный шаг

0.31.1 закрывает package-shape regression Linux asset 0.31.0: опубликованный binary archive не содержит обязательный `scripts/migrate.sh`. Corrective patch должен пройти archive-membership gate, clean install, `0.30.0 → 0.31.1`, `0.31.0 → 0.31.1`, PostgreSQL migration/restart, idempotency, recovery и release-metadata checks. Existing `v0.31.0` tag/release/assets не переписываются.

Следующие публичные направления после закрытия corrective release: Health / Incidents / Audit / Reports; полный Identity/RBAC/Security UI; Managed Network; Node lifecycle; Recovery/HA; Market modules; Capacity automation; Mobile; затем accessibility/security/performance и production hardening до 1.0.0.

Каждый release train должен завершаться официальным Public Stable. Релиз не публикуется при release-blocking defect, false Success, нерешённой high-risk security/recovery проблеме, неподтверждённом install/upgrade/rollback path либо расхождении документации с фактическим поведением.
