# Control Center — продуктовая дорожная карта

Статус: **CURRENT**. Текущий опубликованный Public Stable — **0.31.0**. Corrective release **0.31.1** находится в qualification и не считается Public Stable до отдельной публикации.

Опубликованная основа включает Identity/RBAC/Audit, Site/Network foundation, Capacity Intelligence foundation, Session Security, recovery/network evidence freshness, Product Web Shell, Sites / Nodes / Inventory и безопасный Changes / Jobs operational workflow с approval, durable Job, verification и recovery evidence.

## Ближайший обязательный release step

0.31.1 закрывает package-shape regression Linux Stable 0.31.0: опубликованный binary archive 0.31.0 не содержит обязательный migration runner. Corrective patch не расширяет product scope и должен пройти полный install/update/recovery/security release cycle, включая archive-membership gate, clean install, upgrade с 0.30.x и 0.31.0, PostgreSQL migration/restart, сохранение данных/настроек/admin credential state, rollback/forward-recovery и проверку публичного installer/update flow.

Следующие публичные направления после закрытия corrective release: Health / Incidents / Audit / Reports; полный Identity/RBAC/Security UI; Managed Network; Node lifecycle; Recovery/HA; Market modules; Capacity automation; Mobile; затем accessibility/security/performance и production hardening до 1.0.0.

Каждый release train должен завершаться официальным Public Stable. Релиз не публикуется при release-blocking defect, false Success, нерешённой high-risk security/recovery проблеме, неподтверждённом install/upgrade/rollback path либо расхождении документации с фактическим поведением.
