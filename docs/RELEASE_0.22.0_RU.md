# Control Center 0.22.0

Статус: официальный релиз; qualification и release gates пройдены.

Версия 0.22.0 добавляет завершённый контур безопасности локальных пользовательских сессий Control Center: ограниченный абсолютный срок жизни, idle timeout, просмотр эффективной политики, инвентаризацию и отзыв собственных сессий с durable persistence и обязательным Audit.

## Session Security Policy

Политика сессий задаётся параметрами `CC_AUTH_SESSION_TTL` и `CC_AUTH_SESSION_IDLE_TIMEOUT`. Абсолютный TTL ограничен диапазоном от 1 минуты до 7 суток; idle timeout не может быть меньше 1 минуты или превышать абсолютный TTL. Некорректная конфигурация отклоняется fail-closed при запуске.

Аутентифицированный `GET /api/v1/auth/session-policy` возвращает только эффективные безопасные параметры политики. Активность может переносить только idle deadline и никогда не продлевает исходный абсолютный срок жизни сессии.

## Управление сессиями

Контур включает инвентаризацию собственных действующих сессий и их отзыв. Состояние `last_activity_at` сохраняется в durable session storage, поэтому рестарт Control Center не обнуляет idle timeout и не восстанавливает истёкшую сессию.

Для PostgreSQL добавлена миграция `0009_auth_session_activity` и qualification-тесты установки/обновления схемы и persistence поведения.

## Audit и безопасность

Чтение effective session policy и операции управления сессиями создают обязательное audit evidence без session token, cookie или другого credential material. Если обязательная запись Audit недоступна, защищённая операция завершается fail-closed.

First-login ограничение сохраняется: до обязательной смены первоначального пароля обычные session-management операции не становятся обходным путём к нормальной работе системы.

Добавлены unit, HTTP API, persistence и migration tests для TTL/idle policy, session inventory, activity persistence, revocation и fail-closed audit semantics.

Qualification и release gates для официального выпуска 0.22.0 пройдены.
