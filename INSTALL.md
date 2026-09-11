# Установка Control Center 0.24.0

## Требования

- Linux AMD64 с systemd и `systemd-run`;
- PostgreSQL 15, 16, 17 или 18;
- `psql`, `sha256sum`, `curl` и `tar`;
- HTTPS reverse proxy для удалённого или браузерного доступа.

## Загрузка и проверка

```sh
version=0.24.0
curl -fL -o "control-center-$version-linux-amd64.tar.gz" \
  "https://github.com/ControlCenterSoft/control-center-stable/releases/download/v$version/control-center-$version-linux-amd64.tar.gz"
curl -fL -o "control-center-$version-linux-amd64.tar.gz.sha256" \
  "https://github.com/ControlCenterSoft/control-center-stable/releases/download/v$version/control-center-$version-linux-amd64.tar.gz.sha256"
sha256sum -c "control-center-$version-linux-amd64.tar.gz.sha256"
tar -xzf "control-center-$version-linux-amd64.tar.gz"
```

Если checksum не совпадает, установку необходимо прекратить.

## Установка файлов

```sh
sudo useradd --system --home-dir /var/lib/control-center \
  --create-home --shell /usr/sbin/nologin control-center 2>/dev/null || true
sudo install -d -o root -g root -m 0755 /opt/control-center/releases/0.24.0
sudo cp -a control-center-0.24.0/. /opt/control-center/releases/0.24.0/
sudo ln -sfn /opt/control-center/releases/0.24.0 /opt/control-center/current
sudo install -d -o root -g root -m 0755 /etc/control-center
sudo install -o root -g root -m 0600 \
  /opt/control-center/current/config/control-center.env.example \
  /etc/control-center/control-center.env
sudo install -o root -g root -m 0644 \
  /opt/control-center/current/deploy/systemd/control-center.service \
  /etc/systemd/system/control-center.service
```

Отредактируйте `/etc/control-center/control-center.env`: укажите PostgreSQL и другие параметры вашей среды. Файл должен оставаться доступным только администратору (`root:root`, `0600`).

## Подготовка базы данных

Перед обновлением существующей установки обязательно сделайте резервную копию PostgreSQL.

Для применения forward migrations используется тот же `EnvironmentFile`, что и для сервиса:

```sh
sudo systemd-run --wait --pipe --collect \
  --service-type=oneshot \
  --uid=control-center \
  --gid=control-center \
  --property=NoNewPrivileges=yes \
  --property=PrivateTmp=yes \
  --property=ProtectSystem=strict \
  --property=ProtectHome=yes \
  --property=EnvironmentFile=/etc/control-center/control-center.env \
  --setenv=MIGRATIONS_DIR=/opt/control-center/current/migrations \
  -- /opt/control-center/current/scripts/migrate.sh
```

Migration runner проверяет уже применённые миграции и прекращает работу при несовпадении ожидаемой целостности.

## Запуск и проверка

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now control-center
curl --fail --silent --show-error http://127.0.0.1:8080/health/live
curl --fail --silent --show-error http://127.0.0.1:8080/health/ready
```

Для чистой установки используйте первоначальную учётную запись `admin` / `admin`. Первый вход должен завершаться обязательной сменой пароля; до смены пароля обычная работа запрещена.

Не публикуйте loopback listener напрямую в Интернет. Для внешнего доступа используйте HTTPS reverse proxy и ограничивайте ingress авторизованными пользователями. PostgreSQL не должен быть публично доступен.

## Обновление

1. Сделайте резервную копию PostgreSQL и конфигурации Control Center.
2. Остановите сервис.
3. Установите новую версию в отдельный каталог `/opt/control-center/releases/<version>`.
4. Примените forward migrations новой версии.
5. Переключите `/opt/control-center/current` на новый каталог.
6. Запустите сервис.
7. Проверьте health/readiness, вход существующего администратора и критичные операции чтения.

При обновлении пароль администратора сохраняется и **не сбрасывается** на `admin`.

## Rollback

Rollback выполняйте только вместе с совместимым состоянием базы данных. Если новая версия уже изменила схему или данные, сначала восстановите соответствующий backup PostgreSQL, а затем переключайте `/opt/control-center/current` на предыдущую версию.
