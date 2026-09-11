# Control Center 0.27.0 Stable

Статус выпуска: **Stable**.

## Главное изменение

Control Center 0.27.0 добавляет детерминированную read-only модель актуальности подтверждённых изолированных restore drill. Система связывает freshness assessment с точной версией restore metadata и digest проверочного evidence, формирует bounded snapshot состояния `FRESH`, `STALE` или `UNVERIFIABLE` и повторно проверяет lineage при изменении исходных данных.

Это устраняет риск повторного использования старого, формально корректного evidence после изменения restore state или появления более нового успешного restore drill.

## Информационная безопасность

- новые контракты работают fail-closed для невалидного, подменённого, устаревшего или неподтверждаемого evidence;
- функции являются advisory/read-only и не предоставляют полномочий на backup, restore, fencing, retry или изменение production state;
- не добавлены новые сетевые listeners, credential flow, хранение секретов или внешние runtime-зависимости;
- опубликованные SQL migrations предыдущего Stable защищены от изменения byte-for-byte.

## Совместимость и обновление

Версия 0.27.0 не добавляет SQL migration и сохраняет существующие данные и пароль администратора при обновлении. Проверены clean-install и supported-upgrade сценарии на PostgreSQL 15, 16, 17 и 18, restart/persistence boundaries, race/static/unit/build проверки и воспроизводимая упаковка Linux AMD64.

## Аутентификация

При чистой установке создаётся `admin` / `admin` с обязательной сменой пароля при первом входе. До смены пароля обычная работа запрещена. Обновление не сбрасывает пользовательский пароль.
