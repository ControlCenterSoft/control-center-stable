# Control Center 0.31.1 — PUBLIC STABLE

0.31.1 — официальный корректирующий Public Stable release линии 0.31.x. Он не расширяет функциональный scope Control Center и исправляет release-package regression опубликованного 0.31.0.

## Исправлено

Linux AMD64 package снова содержит обязательный исполняемый `scripts/migrate.sh`. Перед публикацией точная release identity проходит проверку фактического состава tar.gz, clean install, поддерживаемых upgrade paths 0.30.0 → 0.31.1 и 0.31.0 → 0.31.1, повторного применения migrations, race/unit/build/public-source gates и детерминированной сборки.

Опубликованный `v0.31.0` и его assets не изменяются. Для перехода с установленного 0.31.0 используется только bounded compatibility path, привязанный к неизменяемой identity 0.31.0 и проверяемым SHA-256.

## Сохраняемая функциональность 0.31

- exact immutable Change revision, semantic diff и blast radius;
- execution preflight и approval evidence;
- durable Job lifecycle/timeline;
- reconnect, version-bound cancel и bounded manual retry;
- terminal result/post-condition и recovery evidence;
- fail-closed защита от false Success, stale/mismatched evidence и migration drift.

## Артефакты релиза

Публикуемый набор содержит Linux AMD64 archive, отдельный SHA-256 sidecar, source archive, CycloneDX SBOM, THIRD_PARTY_NOTICES, qualification evidence, provenance, release manifest и `SHA256SUMS`. Все метаданные привязаны к точной immutable revision релиза.

## Аутентификация и данные

Clean install сохраняет bootstrap-контракт локального `admin/admin` только для первого входа с обязательной сменой пароля. Upgrade не сбрасывает установленный пользователем пароль администратора и не должен терять пользовательские данные или конфигурацию.

## Коммерческий контур

Commercial/legal launch является отдельным контуром. Технический Public Stable не является заявлением о завершённой коммерческой/юридической легализации продукта.
