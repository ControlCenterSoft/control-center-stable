# Control Center 0.28.0 Stable

Статус выпуска: **Stable**.

## Главное изменение

Control Center 0.28.0 усиливает безопасный контур сетевых изменений. Connectivity verification evidence теперь жёстко связано с точными `plan_id` и `revision_id`, ограничено freshness budget и повторно проверяется непосредственно на границе перехода из preflight.

Сериализованный admission остаётся детерминированным снимком результата, но не считается доверенным источником истины: перед переходом система повторно вычисляет identity authoritative evidence, проверяет его актуальность и требует точного соответствия срока действия admission сроку исходного evidence.

## Информационная безопасность

- `ready=true` не предоставляет полномочий на выполнение сетевого изменения;
- stale, mixed-plan, malformed, tampered или неполное evidence отклоняется fail-closed;
- самосогласованный admission с подменённым `evidence_id` или искусственно продлённым `expires_at` не может открыть apply window;
- staged apply, connectivity verification и rollback остаются отдельными обязательными стадиями;
- WAN/LAN-конфигурация сама по себе не включает routing, NAT или port-forwarding;
- выпуск не добавляет новый network listener, credential flow, secret storage или внешний runtime dependency.

## Совместимость и коммерческая граница

Версия 0.28.0 не добавляет SQL migration, не меняет dependency graph сторонних runtime-компонентов и сохраняет существующие данные и пароль администратора при обновлении. Новых обязательств по redistribution, source-offer или NOTICE из-за scope 0.28.0 не возникает. Существующие license/SPDX и distribution/compliance требования Market сохраняются.

Независимая Stable qualification подтверждает clean-install и supported-upgrade на PostgreSQL 15, 16, 17 и 18, adapter/restart recovery, race/static/unit/build проверки, public-source safety и воспроизводимую Linux AMD64 упаковку.

## Аутентификация

При чистой установке создаётся `admin` / `admin` с обязательной сменой пароля при первом входе. До смены пароля обычная работа запрещена. Обновление не сбрасывает пользовательский пароль.
