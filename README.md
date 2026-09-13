# Control Center Stable channel

Control Center — самостоятельная платформа управления инфраструктурой для администраторов. Этот репозиторий является официальным публичным Stable-каналом продукта.

**Текущий Public Stable: 0.31.1.** Официальный релиз `v0.31.1` опубликован с исправленным Linux AMD64 package contract и полным набором release artifacts.

Линия 0.31 содержит безопасный operational workflow Changes / Jobs: точную revision изменения, semantic diff и blast radius, preflight, approval evidence, durable Job lifecycle, reconnect/cancel/retry с version-bound проверками, подтверждение результата и recovery evidence. Неизвестное, устаревшее или неполное evidence не отображается как Success.

0.31.1 является corrective patch без расширения feature scope. Linux archive содержит обязательный исполняемый `scripts/migrate.sh`; clean install и поддерживаемые переходы с 0.30.0 и 0.31.0 прошли qualification. Опубликованный `v0.31.0` сохраняется неизменяемым как исторический release с известным package-shape defect и не переписывается задним числом.

После чистой установки создаётся локальный пользователь `admin` с первоначальным паролем `admin`. При первом входе обязательна смена пароля; обновление существующей установки сохраняет установленный пользователем пароль.

Документы: [установка и обновление](INSTALL.md), [примечания к выпуску](RELEASE_NOTES.md), [безопасность](SECURITY.md), [архитектура](ARCHITECTURE.md), [дорожная карта](ROADMAP.md), [манифест выпуска](RELEASE-MANIFEST.json).
