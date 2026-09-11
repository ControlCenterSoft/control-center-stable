# Control Center 0.27.0 — актуальность проверок восстановления

Статус: **release candidate**. Базовая версия Public Stable — **0.26.0**. Идентичность кандидата 0.27.0 зафиксирована; публикация допускается только после успешной qualification точного итогового SHA и последующих release/public-stable gates.

## Назначение

Версия 0.27.0 добавляет детерминированную read-only модель актуальности реально проверенного изолированного восстановления. Она позволяет отличать свежую проверку восстановления от устаревшей и не считать один лишь исторический статус `SUCCEEDED` достаточным доказательством текущей готовности к восстановлению.

## Restore Drill Freshness Assessment

Контракт `recovery.restore-drill-freshness/v1` принимает только валидный `ISOLATED_DRILL` со статусом `SUCCEEDED` и `Verification=PASSED`, связывает оценку с exact `restore_id`, целевым объектом и временем подтверждённой проверки.

Оператор или policy layer задаёт допустимый возраст проверки в целых секундах. Допустимое окно ограничено одним годом. На границе `verified_at + max_age` результат уже считается `STALE`.

`assessment_id` формируется детерминированно из полного freshness claim: restore identity, target, временных границ, policy window, результата и non-authorizing flags. При чтении сохранённого snapshot идентификатор пересчитывается; подмена даже формально корректно выглядящего `assessment_id` отклоняется fail-closed.

## Exact evidence binding

Контракт `recovery.restore-drill-evidence-binding/v1` связывает freshness assessment с точной текущей версией restore metadata и доказательствами проверки:

- `restore_resource_version`;
- `restore_generation`;
- digest канонического набора verification evidence;
- `verified_at`;
- детерминированный `binding_id`.

Повторная проверка binding пересобирает ожидаемое доказательство из текущего restore state. Изменение resource version, generation, verification evidence или assessment делает старый binding `not_current`, а не позволяет повторно использовать устаревшее доказательство.

## Freshness Snapshot

Контракт `recovery.restore-drill-freshness-snapshot/v1` представляет bounded read-only состояние для точного target:

- `FRESH` — последнее валидное успешное restore-drill evidence ещё находится в freshness window;
- `STALE` — доказательство было валидным, но freshness window истёк;
- `UNVERIFIABLE` — для точного target нет доказуемого валидного успешного изолированного restore drill.

При нескольких restore records выбирается самое позднее валидное `verified_at`; равные timestamps разрешаются детерминированно по `restore_id`. Невалидные, failed и non-isolated записи не становятся freshness evidence.

Snapshot хранит exact lineage: restore ID/resource version/generation, assessment ID и digest verification evidence. Добавлены два уровня revalidation:

1. проверка snapshot против одного точного текущего restore record — защищает от resource-version/generation/evidence drift;
2. проверка snapshot против authoritative current restore set — защищает от повторного использования старого self-consistent snapshot после появления более нового успешного restore-drill evidence.

`UNVERIFIABLE` не может быть подтверждён проверкой одного restore record, потому что один объект не доказывает отсутствие других валидных evidence. Он может быть подтверждён только относительно authoritative набора restore records для target.

## Информационная безопасность

Новые контракты являются только evidence и не расширяют полномочия выполнения:

- `advisory_only=true`;
- `production_mutation=false`;
- не запускают backup или restore;
- не выполняют fencing;
- не изменяют RecoveryPoint, Desired State или Actual State;
- не предоставляют retry/execution/restore authority;
- не добавляют сетевой listener, новый credential flow или хранение секретов;
- не добавляют новый внешний runtime dependency.

Невалидная restore metadata, непроверенный или неуспешный restore, non-isolated режим, некорректное временное окно, `checked_at` раньше `verified_at`, подмена assessment identity и drift exact evidence отклоняются fail-closed. Устаревшая или неподтверждаемая проверка не исправляется автоматически и требует отдельного управляемого действия согласно политике эксплуатации.

## Коммерческая и лицензионная граница

Изменения 0.27.0 не добавляют сторонних runtime-компонентов и не меняют dependency graph продукта: scope состоит из собственных Go-контрактов/валидации, JSON Schema, тестов и русскоязычной документации. Новых обязательств по redistribution, source-offer или NOTICE из-за этого scope не возникает. Существующие требования Market к license/SPDX expression, authoritative source, distribution mode, commercial/redistribution disposition и versioned evidence сохраняются без ослабления.

Публичная публикация всё равно должна пройти штатные license/compliance и artifact/provenance gates; этот раздел не заменяет машинно проверяемые release evidence.

## Install / upgrade / recovery compatibility

0.27.0 не добавляет новую SQL migration и не изменяет опубликованные migration-файлы 0.26.0. В candidate включена byte-for-byte защита миграций Public Stable 0.26.0 от изменения. Перед выпуском точный итоговый SHA обязан заново пройти штатные clean-install и supported-upgrade проверки на поддерживаемых PostgreSQL, adapter/restart qualification, race/static/unit/build и public-source safety gates.

Изменения являются read-only recovery evidence и сами по себе не меняют процедуру backup/restore, существующие данные или пользовательские credentials.

## Packaging и публикация

Наличие исходного кода, release notes или зелёных проверок предыдущего SHA не является основанием для публикации. Для выпуска требуются зелёные проверки точного итогового SHA, merge в канонический release path, официальный tag/release и отдельная Public Stable promotion с предусмотренными binary/source artifacts, SHA-256 checksums, qualification/release manifests и provenance.

Gemini-review является дополнительным неавторитетным review-сигналом и не заменяет deterministic CI, security qualification, branch protection или обязательные release gates. Недоступность внешнего review-провайдера сама по себе не должна подменять результат детерминированных проверок.

## Граница готовности

Версия считается готовой к promotion только после qualification **точного итогового SHA 0.27.0**. Нельзя переносить PASS от предыдущего SHA после изменения release identity или metadata. До завершения этого цикла 0.27.0 остаётся release candidate и не должна описываться как Public Stable.
