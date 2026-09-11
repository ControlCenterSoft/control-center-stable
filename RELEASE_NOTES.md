# Control Center 0.24.0 Stable

Статус: **stable**.

Control Center 0.24.0 переводит актуальную официальную линию 0.24.0 в стабильный бинарный канал.

## Основные изменения стабильной линии

- локальная аутентификация с обязательной сменой первоначального пароля;
- сохранение пользовательского пароля администратора при обновлении;
- защищённые сессии и RBAC self-introspection;
- Audit и типизированные security boundaries;
- Desired/Actual State, Changes/Jobs и проверяемые операции;
- lifecycle/recovery contracts;
- Inventory, Market, PXE, Automation, Domain и Integration foundations;
- network/capacity/placement evidence contracts;
- bounded-защита локального входа от brute force и credential spraying.

## Защита входа в 0.24.0

Для неуспешных попыток локального входа используются независимые account- и source-scoped границы. Неверный пароль, неизвестный пользователь и временно заблокированный вход сохраняют единый внешний ответ `invalid_credentials`, чтобы не раскрывать существование учётной записи или состояние защиты.

Состояние защиты ограничено по памяти и не хранит пароли, session token или token digest. Реализация 0.24.0 является process-local и не заявляется как cluster-wide rate limiting.

## Стабильная квалификация

Перед публикацией стабильной версии подтверждены:

- format/vet/unit/build проверки;
- clean-install PostgreSQL 15, 16, 17 и 18;
- поддержанный upgrade-path PostgreSQL 15, 16, 17 и 18;
- PostgreSQL adapter/restart проверки на 15–18;
- race detector;
- проверка публичной границы на credentials/private-key material;
- повторная проверка сформированного stable-tree;
- воспроизводимая сборка Linux AMD64 и SHA-256 checksum.

## Первый вход и обновление

На чистой установке первоначальная учётная запись: `admin` / `admin`. Пароль необходимо сменить при первом входе; до этого обычная работа запрещена.

При обновлении существующий пароль администратора сохраняется и не заменяется первоначальным паролем.

Перед любым обновлением создавайте резервную копию PostgreSQL и конфигурации Control Center.
