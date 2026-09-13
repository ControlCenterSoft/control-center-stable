# Control Center 0.31.1 Stable corrective patch

Статус: **candidate for Public Stable qualification**.

0.31.1 — корректирующий patch поверх официального Public Stable 0.31.0. Он не добавляет новый продуктовый feature scope и не изменяет Changes / Jobs operational semantics. Цель выпуска — восстановить подтверждённый Linux install/update package contract после обнаруженного дефекта опубликованного binary asset 0.31.0.

## Исправление

Опубликованный `control-center-0.31.0-linux-amd64.tar.gz` имеет корректную SHA-256, но не содержит обязательный `scripts/migrate.sh`. Поэтому общий binary install/update path через этот archive не считается самостоятельным подтверждённым путём; защитные проверки должны оставаться fail-closed.

В 0.31.1 Linux package обязан содержать исполняемый `scripts/migrate.sh`. Qualification проверяет не только исходный build contract, но и фактический состав собранного архива после extraction, а затем использует именно bundled migration runner для clean-install и supported upgrade acceptance.

Tag/release/assets `v0.31.0` остаются неизменяемыми и не переписываются.

## Сохраняемая функциональность 0.31

- exact immutable Change revision, semantic diff и blast radius;
- execution preflight и approval evidence;
- durable Job lifecycle/timeline;
- reconnect, version-bound cancel и bounded manual retry;
- terminal result/post-condition и recovery evidence;
- fail-closed защита от false Success, stale/mismatched evidence и migration drift.

## Обязательная qualification перед Public Stable

0.31.1 может быть опубликован как Public Stable только после PASS точной итоговой release identity минимум по следующим границам:

- public-source/no-secret boundary;
- format/vet, unit/contracts, race и build;
- воспроизводимая Linux AMD64 упаковка и SHA-256 sidecar;
- фактическое наличие и executable bit `scripts/migrate.sh` внутри извлечённого release archive;
- clean install PostgreSQL через migration runner из собранного archive;
- supported upgrade с предыдущего Stable baseline и с поддерживаемых установленных 0.31.0 состояний, где применимо;
- повторное применение migrations/idempotency без destructive downgrade;
- сохранение пользовательских данных, конфигурации и установленного пароля администратора;
- rollback/forward-recovery;
- сохранение immutable опубликованных migrations;
- release identity/version consistency;
- final release metadata/checksums/provenance/SBOM для публикуемого asset set.

Commercial/legal launch остаётся отдельным контуром и не подменяет техническое release evidence.
