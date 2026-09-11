# RecoveryPoint, Backup и Restore — metadata contracts

Статус: контракт Control Center 0.4. Machine-readable схема: [`../api/openapi-recovery-metadata.yaml`](../api/openapi-recovery-metadata.yaml).

## Граница реализации

Этот пакет определяет только типизированные метаданные и правила их проверки. Создание структуры Go или JSON-документа:

- не запускает backup или restore;
- не обращается к backup repository;
- не изменяет файлы, БД, сеть, firewall, маршрутизацию или storage;
- не выполняет fencing;
- не является подтверждением успешного восстановления.

Runtime Recovery Manager, provider execution, persistence/API и реальные restore drills внедряются отдельными этапами дорожной карты.

## Общая модель

Все верхнеуровневые объекты встраивают единый `corecontracts.ObjectMetadata`: `object_id`, `scope_id`, `owner_scope`, `generation`, непрозрачный строковый `resource_version`, `created_at` и `updated_at`. `resource_version` выдаёт persistence/sync слой; клиент не должен его разбирать, сортировать или увеличивать. Update/delete обязан использовать общий `ObjectPrecondition`, а storage adapter — атомарно проверять precondition и правила `ValidateSuccessor`. Проверка того, что `owner_scope` равен целевому scope либо является его допустимым предком, также выполняется вызывающим topology/storage adapter до записи.

Вложенные ссылки на защищаемые объекты обязаны оставаться в том же scope: контракт не допускает неявную cross-scope операцию. Временные отметки должны быть UTC, идти в непротиворечивом порядке и не описывать событие позже `updated_at` самого объекта. Неизвестные и повторяющиеся JSON-поля, пропущенные обязательные вложенные поля, явный `null` вместо типизированного значения, дополнительные JSON-значения, некорректные enum, дублирующиеся ссылки и документы более 1 MiB отклоняются.

`ObjectReference` различает Node, Service, Database, Configuration, User, Group, Policy, Application, Site и Control Plane, а также явно указывает `STATELESS` или `STATEFUL`. Это позволяет object-level recovery не требовать отката всей production DB.

## RecoveryPoint

Schema: `recovery.point/v1`.

RecoveryPoint задаёт одну логическую consistency boundary и содержит ссылки на защищаемые объекты и backup metadata. Поддерживаются triggers `MANUAL`, `SCHEDULED`, `PRE_CHANGE`, `PRE_DESTRUCTIVE` и consistency levels `CRASH_CONSISTENT`, `APPLICATION_CONSISTENT`, `OBJECT_VERSIONED`.

Инварианты состояния:

- `CREATING` может ещё не иметь backup IDs и не содержит failure result;
- `READY` имеет минимум один backup ID и не содержит failure;
- `PARTIAL` имеет минимум один backup ID и failure metadata;
- `FAILED` обязательно содержит стабильный error code и безопасное описание ошибки;
- `EXPIRED` имеет уже наступивший `expires_at`;
- elapsed recovery point не может продолжать называться `READY`;
- `PRE_CHANGE` и `PRE_DESTRUCTIVE` обязаны ссылаться на `change_id`.

## BackupMetadata

Schema: `recovery.backup/v1`.

Объект отделяет результат захвата данных от доказанной восстанавливаемости. `state=COMPLETED` означает, что provider завершил операцию и вернул проверяемую artifact metadata. Это не означает `health=VERIFIED`.

Поддерживаются FULL, INCREMENTAL, DIFFERENTIAL, SNAPSHOT, BASE_WAL и OBJECT_VERSION. Incremental/differential backup ссылается на другой parent backup. BASE_WAL требует PITR window и capabilities `BASE_BACKUP`, `WAL_ARCHIVE`, `PITR`; PostgreSQL LSN должны образовывать возрастающий диапазон.

Artifact содержит только repository ID, безопасный repository-relative object key, размер, SHA-256 digest и режим шифрования. Credentials, endpoint, абсолютный путь и содержимое ключа запрещены. Для customer-managed encryption хранится только ID ключа.

`BackupHealth` имеет отдельные состояния:

- `UNKNOWN` — операция ещё не завершена;
- `UNVERIFIED` — backup завершён, но restore не доказан;
- `VERIFIED` — есть restore ID, время проверки и immutable `RESTORE_DRILL` + `FUNCTIONAL_TEST` evidence;
- `DEGRADED` — фактический restore drill выявил проблему; сохраняются restore ID, время проверки и evidence;
- `FAILED` — сама backup operation завершилась ошибкой.

Таким образом, успешный Job или наличие файла не превращаются в зелёный Backup Health.

## RestoreMetadata

Schema: `recovery.restore/v1`.

Режимы: `IN_PLACE`, `ALTERNATE_TARGET`, `ISOLATED_DRILL`, `OBJECT_LEVEL`, `PITR`. Restore всегда ссылается на RecoveryPoint и один или несколько Backup IDs. OBJECT_LEVEL перечисляет конкретные объекты; PITR содержит целевое время, которое не может находиться позже времени запроса.

Состояния PLANNED/RUNNING/VERIFYING/SUCCEEDED/FAILED/CANCELLED согласуются с timestamps, provider operation, failure и verification metadata. `SUCCEEDED` разрешён только после `verification.outcome=PASSED` с evidence. Verification evidence не может быть датирован позже `verified_at`, а проверка — раньше запуска restore. Успешный isolated drill дополнительно требует и `RESTORE_DRILL`, и `FUNCTIONAL_TEST` evidence.

## Provider metadata

Provider описывается через `provider_id`, `adapter_id`, semver, repository reference, opaque operation ID и allowlisted capabilities. В структуре намеренно отсутствуют credentials, tokens, host/endpoint, shell command и произвольная исполняемая конфигурация.

Capability проверяется относительно операции: backup требует `BACKUP`, restore — `RESTORE`, snapshot/PITR/object-level/drill требуют соответствующие дополнительные возможности.

## Fencing

Fencing metadata имеет состояния `NOT_REQUIRED`, `REQUIRED`, `CONFIRMED`, `FAILED`.

- `NOT_REQUIRED` не может содержать provider operation или скрытую команду;
- `REQUIRED` фиксирует план, targets и монотонный epoch, но ещё не результат;
- `CONFIRMED` требует FENCING-capable provider, operation ID, timestamp и `FENCING_CONFIRMATION` evidence;
- `FAILED` требует provider operation и failure metadata.

Stateful IN_PLACE/PITR restore не может перейти к исполнению до `CONFIRMED`. Подтверждение fencing и его evidence относятся к текущему restore request, а подтверждение должно предшествовать `started_at` restore. Если fencing завершился ошибкой, metadata restore не может содержать признаки запуска provider restore. Сам metadata contract fencing не выполняет.

## RPO/RTO evidence

Schema: `recovery.objective-evidence/v1`.

`RecoveryObjectiveEvidence` связывает policy target, RecoveryPoint, Restore, защищаемый объект, наблюдаемые RPO/RTO и immutable evidence. `PASSED` требует:

- фактически измеренные RPO и RTO;
- значения не выше policy targets;
- evidence реального restore drill;
- functional-test evidence;
- отсутствие failure reason.

`FAILED` содержит превышенное значение и/или явную причину. Evidence не может быть датировано позже итогового измерения. Это обеспечивает требование: RPO/RTO и Backup Health подтверждаются фактом восстановления, а не декларацией provider.

## Aggregate integrity

Проверка отдельного JSON-документа не доказывает существование его ссылок. `ValidateRecoveryMetadataGraph` поэтому принимает полный read-consistent snapshot из четырёх обязательных коллекций: RecoveryPoint, BackupMetadata, RestoreMetadata и RecoveryObjectiveEvidence. Пустая коллекция передаётся как non-nil пустой Go slice; `nil` означает недоступную или частичную projection и отклоняется fail-closed.

Aggregate validator дополнительно доказывает:

- глобальную уникальность recovery object IDs, provider operation claims и repository artifact keys;
- двустороннюю согласованность RecoveryPoint/Backup, совпадение scope/owner и полное backup-покрытие объектов для `READY`;
- существование parent backup, отсутствие циклов, совместимость target/provider/repository и строгий временной порядок incremental/differential chains;
- принадлежность Restore указанному RecoveryPoint, наличие доступных completed artifacts, совместимость source/target/provider и покрытие PITR target реальным BASE_WAL window;
- происхождение Backup Health и RPO/RTO evidence из конкретного завершённого isolated restore drill: один evidence ID имеет ровно один verification/fencing origin, а переносимая immutable ссылка должна совпадать с ним полностью;
- невозможность заявить `VERIFIED` или `PASSED` на основе failed restore, а также невозможность указать observed RTO меньше фактической длительности связанного restore.

Validator не заменяет атомарное чтение persistence adapter: вызывающий слой обязан сформировать snapshot в одной согласованной ревизии. Существование `policy_id`, topology ancestry для `owner_scope` и существование внешних защищаемых объектов проверяются соответствующими policy/topology projections до записи; внутри recovery graph все связанные записи требуют одинаковых `scope_id` и `owner_scope`.

## Совместимость с 0.3.x

В 0.3.x отсутствовали типизированные RecoveryPoint, BackupMetadata, RestoreMetadata и RPO/RTO evidence. Существовали общие Jobs, Resources и Config Revisions, но их состояние не доказывает наличие корректного backup artifact или успешного restore.

`MigrateV03Metadata` реализует консервативную детерминированную границу:

- пустое legacy state даёт стабильные пустые массивы нового контракта;
- ссылки на legacy JOB/RESOURCE/CONFIG_REVISION сортируются по kind/ID;
- каждая ссылка получает `REQUIRES_REVIEW` и сохраняется в quarantine report;
- ни одна legacy запись автоматически не становится RecoveryPoint, Backup, Restore или recovery evidence;
- дубликаты, неизвестные kind и неканонические ID отклоняются.

Ручное сопоставление или архивирование legacy-записей будет отдельной явно подтверждаемой операцией. Автоматического продвижения или скрытого запуска нет.

## Go API

- `ValidateRecoveryPoint`;
- `ValidateProviderMetadata` и `ValidateFencingMetadata`;
- `ValidateBackupMetadata`;
- `ValidateRestoreMetadata`;
- `ValidateRecoveryObjectiveEvidence`;
- `ValidateRecoveryMetadataGraph` для полного read-consistent snapshot и межобъектной provenance validation;
- строгие decoders для RecoveryPoint, Provider/Fencing, Backup, Restore и RecoveryObjectiveEvidence;
- `MigrateV03Metadata` для compatibility report.

Контрактные тесты покрывают положительные сценарии, несовместимые state transitions, timestamp ordering, PITR, repository-safe artifacts, encryption key references, provider capabilities, Backup Health evidence, fencing gates, object-level restore, restore drills, RPO/RTO claims, отсутствующие/перекрёстные ссылки, orphan records, parent cycles, подмену evidence provenance и migration determinism.
