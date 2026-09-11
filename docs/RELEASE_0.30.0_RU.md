# Control Center 0.30.0 — Public Stable

Выпуск 0.30.0 добавляет read-only экраны «Сайты», «Узлы» и «Инвентаризация» и делает состояние evidence явным: current, stale, expired или unavailable.

Безопасность построена по fail-closed принципу: неизвестное состояние не выдаётся за исправное, endpoint не получает mutation/execution authority, глобальная инвентаризация не становится доступна только из-за site-scoped delegated binding, а чувствительные inventory-поля минимизированы.

Обновление с 0.29.0 не добавляет новую SQL migration, сохраняет пользовательские данные, настройки и пароль администратора. Перед обновлением требуется проверяемая резервная копия; после обновления — readiness/health и проверка основных read-only операций.

Независимая Stable qualification подтверждает PostgreSQL 15–18 clean-install и supported-upgrade, adapters/restart recovery, race/static/unit/build проверки и воспроизводимую Linux AMD64 упаковку.
