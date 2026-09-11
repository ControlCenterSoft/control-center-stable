# Control Center 0.5.0

Релиз 0.5.0 закрывает этап Multi-node Operations и Lifecycle поверх
распределённых контрактов 0.4.

В релиз входят:

- one-time bootstrap grants и каноническое enrollment admission с roles,
  scope, site и management zone;
- безопасные Drain/Replace plans со строгой границей stateless/stateful;
- canary-first Upgrade Orchestrator с dependency graph, update rings,
  maintenance window, quorum boundary, rollback и health gates;
- Placement Planner v1: детерминированный выбор по dominant utilization,
  topology/version preconditions, affinity, roles, site/zone и capacity;
- Capacity Planner MVP: safe capacity с N-node failure reserve, bottleneck,
  confidence и advisory action;
- синтетический Agent load harness до 100 000 агентов с drop/restart
  injection, stale-result rejection и проверкой отсутствия false Success.

Все планы требуют approved Change, durable Job, audit и повторную проверку
предусловий перед исполнением. Планировщики не изменяют hosts и не включают
production mutation.

Публикация исходного тега 0.5.0 не является production deployment. Подписание
и promotion инфраструктурных артефактов остаются отдельным защищённым gate.
