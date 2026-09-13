# Control Center Stable channel

Control Center — самостоятельная платформа управления инфраструктурой для администраторов. Этот репозиторий является официальным публичным Stable-каналом продукта.

**Текущий опубликованный Stable: 0.31.0.** Ветка 0.31.1 проходит corrective package qualification и не считается опубликованной до отдельного release/promotion.

0.31 сохраняет безопасный operational workflow Changes / Jobs: точную revision изменения, semantic diff и blast radius, preflight, approval evidence, durable Job lifecycle, reconnect/cancel/retry с version-bound проверками, подтверждение результата и recovery evidence. Неизвестное, устаревшее или неполное evidence не отображается как Success.

> **Известное ограничение Linux package 0.31.0:** опубликованный `control-center-0.31.0-linux-amd64.tar.gz` не содержит обязательный `scripts/migrate.sh`. Поэтому публичный one-command installer/update path для этого asset останавливается fail-closed. Tag/assets 0.31.0 не переписываются.

Corrective candidate 0.31.1 должен восстановить package contract и доказать на runner фактическое наличие migration runner внутри archive, clean install и supported upgrade с предыдущего Stable baseline. До публикации 0.31.1 ограничения 0.31.0 остаются в силе.

После корректной чистой установки создаётся локальный пользователь `admin` с первоначальным паролем `admin`. При первом входе обязательна смена пароля; обновление существующей установки не сбрасывает установленный пользователем пароль.

Документы: [установка и обновление](INSTALL.md), [примечания к выпуску](RELEASE_NOTES.md), [безопасность](SECURITY.md), [архитектура](ARCHITECTURE.md), [дорожная карта](ROADMAP.md), [манифест выпуска](RELEASE-MANIFEST.json).
