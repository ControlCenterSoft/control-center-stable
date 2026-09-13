# Control Center 0.31.0 Stable

Control Center — самостоятельная платформа управления инфраструктурой для администраторов. Этот репозиторий является официальным публичным Stable-каналом продукта.

**Текущая стабильная версия: 0.31.0.**

Выпуск 0.31.0 добавляет основной безопасный operational workflow Changes / Jobs: точную revision изменения, semantic diff и blast radius, preflight, approval evidence, durable Job lifecycle, reconnect/cancel/retry с version-bound проверками, подтверждение результата и recovery evidence. Неизвестное, устаревшее или неполное evidence не отображается как Success.

> **Известное ограничение Linux package 0.31.0:** опубликованный `control-center-0.31.0-linux-amd64.tar.gz` не содержит обязательный `scripts/migrate.sh`. Поэтому публичный one-command installer/update path, который ожидает migration runner внутри binary archive, для этого asset останавливается fail-closed. Не обходите проверку вручную. До corrective patch или отдельной квалифицированной exact-tag fallback procedure используйте ограничения и безопасные действия из [INSTALL.md](INSTALL.md).

После корректной чистой установки создаётся локальный пользователь `admin` с первоначальным паролем `admin`. При первом входе обязательна смена пароля; обновление существующей установки не сбрасывает установленный пользователем пароль.

Документы: [установка и обновление](INSTALL.md), [примечания к выпуску](RELEASE_NOTES.md), [безопасность](SECURITY.md), [архитектура](ARCHITECTURE.md), [дорожная карта](ROADMAP.md), [манифест выпуска](RELEASE-MANIFEST.json).
