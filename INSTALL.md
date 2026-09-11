# Control Center 0.26.0 — установка и обновление

## Требования

- Linux AMD64 с systemd;
- PostgreSQL 15, 16, 17 или 18;
- `psql` для миграций базы данных;
- TLS-терминация перед Control Center для production-доступа.

## Чистая установка

1. Скачайте `control-center-0.26.0-linux-amd64.tar.gz` и проверьте его по соответствующему `.sha256` или `SHA256SUMS`.
2. Распакуйте bundle в `/opt/control-center/releases/0.26.0` и направьте `/opt/control-center/current` на этот каталог.
3. Создайте системную учётную запись `control-center` и рабочий каталог `/var/lib/control-center`.
4. Скопируйте `config/control-center.env.example` в `/etc/control-center/control-center.env`, задайте подключение к PostgreSQL и ограничьте права на файл.
5. Если PostgreSQL уже содержит данные, сначала создайте резервную копию, затем примените forward migrations через `scripts/migrate.sh`.
6. Установите `deploy/systemd/control-center.service`, выполните `systemctl daemon-reload` и запустите сервис.
7. Войдите как `admin` / `admin` и немедленно завершите обязательную смену пароля при первом входе. До успешной смены обычная работа не разрешается.

Поставляемая конфигурация по умолчанию слушает loopback. Не публикуйте bootstrap credential в недоверенную сеть и не открывайте production-доступ без TLS.

## Обновление с более раннего стабильного релиза

1. Создайте резервную копию PostgreSQL и текущей конфигурации Control Center.
2. Остановите сервис.
3. Распакуйте 0.26.0 в новый versioned release-каталог.
4. Примените только forward migrations, используя существующие параметры базы данных. Уже опубликованные migration-файлы не изменяются; новая схема доставляется новой migration.
5. Переключите `/opt/control-center/current` на 0.26.0 и запустите сервис.
6. Проверьте readiness, вход существующего администратора, Audit/Audit Integrity согласно RBAC и критичные чтения managed resources.

Существующий пароль администратора при обновлении сохраняется и не сбрасывается к `admin`. Если post-condition проверки не проходят, не объявляйте обновление успешным: вернитесь к заранее подготовленному recovery/rollback path и восстановите согласованное состояние данных и конфигурации.
