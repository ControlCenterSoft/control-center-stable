# Control Center 0.31.1 Stable corrective patch

Статус: **Public Stable**.

0.31.1 — корректирующий patch поверх 0.31.0. Он не добавляет новый продуктовый feature scope и не изменяет Changes / Jobs operational semantics. Выпуск восстанавливает подтверждённый Linux install/update package contract после обнаруженного дефекта опубликованного binary asset 0.31.0.

## Исправление

Linux AMD64 archive 0.31.1 содержит обязательный исполняемый `scripts/migrate.sh`. Release qualification проверила фактический состав собранного archive, clean install PostgreSQL, migration idempotency и поддерживаемые переходы с 0.30.0 и 0.31.0.

`v0.31.0` и его assets остаются неизменяемыми. Известный package-shape defect этой исторической версии не исправлялся перепубликацией старого release.

## Сохраняемая функциональность 0.31

- exact immutable Change revision, semantic diff и blast radius;
- execution preflight и approval evidence;
- durable Job lifecycle/timeline;
- reconnect, version-bound cancel и bounded manual retry;
- terminal result/post-condition и recovery evidence;
- fail-closed защита от false Success, stale/mismatched evidence и migration drift.

## Release evidence

Опубликован полный Stable asset set: Linux AMD64 package и checksum, source archive, CycloneDX SBOM, third-party notices, qualification evidence, provenance, release manifest и `SHA256SUMS`. После публикации assets повторно скачаны и проверены по контрольным суммам и package-shape contract.

Commercial/legal launch остаётся отдельным контуром и не подменяет техническое release evidence.
