# Control Center Stable Channel

Control Center — самостоятельная платформа управления инфраструктурой для администраторов. Этот репозиторий является официальным публичным Stable-каналом продукта.

**Текущий опубликованный Public Stable: 0.31.0.**

Ветка `hotfix/0.31.1-package-shape` готовит corrective Release Candidate **0.31.1**. До завершения qualification и отдельной публикации 0.31.1 не считается Public Stable и не должен рекламироваться пользователям как доступное стабильное обновление.

Выпуск 0.31.0 добавил основной безопасный operational workflow Changes / Jobs: точную revision изменения, semantic diff и blast radius, preflight, approval evidence, durable Job lifecycle, reconnect/cancel/retry с version-bound проверками, подтверждение результата и recovery evidence. Неизвестное, устаревшее или неполное evidence не отображается как Success.

> **Известное ограничение Linux package 0.31.0:** опубликованный `control-center-0.31.0-linux-amd64.tar.gz` не содержит обязательный `scripts/migrate.sh`. Existing `v0.31.0` tag/release/assets не переписываются. Corrective 0.31.1 обязан устранить package-shape defect и заново пройти install/update/recovery qualification.

После корректной чистой установки создаётся локальный пользователь `admin` с первоначальным паролем `admin`. При первом входе обязательна смена пароля; обновление существующей установки не сбрасывает установленный пользователем пароль.

Документы: [установка и обновление](INSTALL.md), [примечания к выпуску](RELEASE_NOTES.md), [безопасность](SECURITY.md), [архитектура](ARCHITECTURE.md), [дорожная карта](ROADMAP.md), [манифест выпуска](RELEASE-MANIFEST.json).
