# Control Center 0.24.0

Статус: кандидат на qualification, не официальный релиз.

Версия 0.24.0 добавляет bounded process-local защиту локального входа от password brute force и credential spraying без изменения внешнего контракта ошибки аутентификации.

## Login abuse protection

По умолчанию учитываются две независимые границы: 8 неуспешных попыток для нормализованного имени пользователя за 10 минут и 30 попыток с одного source IP за 10 минут. После достижения лимита соответствующий ключ блокируется на 15 минут. Состояние ограничено 10 000 ключами и хранит SHA-256 ключи вместо исходных username/IP.

Wrong password, неизвестный пользователь и временно заблокированный вход возвращают одинаковый `invalid_credentials`, не раскрывая существование учётной записи или состояние защиты. Успешный вход очищает только account-scoped failures; source-scoped история сохраняется против username spraying.

Контур не хранит password, session token или token digest и не меняет существующие first-login/session-security границы. Текущая реализация явно process-local и не заявляется как cluster-wide rate limiting.

Пакет включает targeted unit/HTTP tests для account/source blocking, expiry, bounded memory и anti-enumeration поведения.

Официальный выпуск 0.24.0 допускается только после deterministic qualification и release gates.
