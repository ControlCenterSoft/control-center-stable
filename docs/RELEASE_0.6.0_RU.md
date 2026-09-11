# Control Center 0.6.0

Релиз 0.6.0 формирует безопасную основу Site Autonomy и Network Foundation.

В релиз входят:

- типизированная иерархия Site без жёстко заданной Regional-роли;
- детерминированный Site State Synchronization contract с явным владельцем
  Desired/Actual State и обнаружением конфликтов;
- делегированные offline-операции с локальным admission, ограниченным кэшем и
  детерминированным reconcile после восстановления WAN;
- зоны WAN/LAN/MANAGEMENT/DMZ/CLUSTER/STORAGE/BACKUP;
- запрет межзонной маршрутизации по умолчанию;
- обязательное явное назначение Edge Gateway и отдельная авторизация каждой
  gateway-операции;
- staged network change с проверками и автоматическим rollback при потере
  связности или истечении контрольного окна;
- нормализованная Network Telemetry, которая передаёт ограничения каналов и
  Site в Capacity Planner без применения изменений.

Все сетевые решения остаются plan-only: они не изменяют интерфейсы, routes,
firewall или hosts и не включают production mutation. Локальные Site-операции
должны оставаться внутри делегированного scope, durable Job, approved Change и
audit boundary. Кэш не расширяет authority, reconnect не делает конфликтующие
локальные результаты приоритетнее центрального Desired State, а telemetry не
является разрешением на сетевую мутацию.

Публикация исходного тега 0.6.0 не является production deployment. Применение
сетевой конфигурации и promotion артефактов остаются отдельными защищёнными
операциями.
