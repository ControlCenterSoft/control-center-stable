# Control Center — продуктовая дорожная карта

Статус: **CURRENT**. Текущий официальный Public Stable — **0.32.0**.

Опубликованная основа включает Identity/RBAC/Audit, Site/Network foundation, Capacity Intelligence foundation, Session Security, recovery/network evidence freshness, Product Web Shell, Sites / Nodes / Inventory и безопасный Changes / Jobs operational workflow с approval, durable Job, verification и recovery evidence.

0.32.0 завершает corrective release cycle для package-shape regression 0.31.0. Linux Stable package содержит обязательный `scripts/migrate.sh`; подтверждены clean install, переходы `0.30.0 → 0.32.0` и `0.31.0 → 0.32.0`, migration idempotency и post-publication asset verification. Исторический `v0.31.0` не переписывается.

## Ближайший публичный шаг

Следующая продуктовая линия — 0.32.x: Health / Incidents / Audit / Reports. Она не является Public Stable до собственного полного release cycle.

Далее: полный Identity/RBAC/Security UI; Managed Network; Node lifecycle; Recovery/HA; Market modules; Capacity automation; Mobile; accessibility/security/performance и production hardening до 1.0.0.

Каждый release train должен завершаться официальным Public Stable. Релиз не публикуется при release-blocking defect, false Success, нерешённой high-risk security/recovery проблеме, неподтверждённом install/upgrade/rollback path либо расхождении документации с фактическим поведением.
