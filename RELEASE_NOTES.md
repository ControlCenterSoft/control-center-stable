# Control Center 0.26.0 Stable

Статус релиза: **PUBLIC STABLE**.

Control Center 0.26.0 добавляет permission-gated read-only проверку целостности append-only Audit-цепочки. Успешный ответ возвращает только ограниченное агрегированное evidence, обнаруживает повреждение данных и разрывы цепочки, работает fail-closed при ошибках чтения, декодирования, hash/link verification и записывает успешную проверку в Audit до возврата результата.

Также добавлена migration `0010_legacy_03_schema_compatibility` для поддерживаемых legacy PostgreSQL-схем. Ранее опубликованные migration-файлы не переписываются.

Stable qualification подтвердила provenance/public-safety, format/vet/unit/contracts/build, clean-install и supported-upgrade для PostgreSQL 15–18, PostgreSQL adapter/restart checks, race detection, детерминированную Linux AMD64 упаковку и финальный stable gate.

Чистая установка создаёт `admin` / `admin` с обязательной сменой пароля при первом входе. Обновление сохраняет существующий пароль администратора и не сбрасывает его к первоначальному значению.

В публичных release notes не должны раскрываться внутренние процессы разработки, runner-инфраструктура, внутренние адреса, секреты или названия внутренних AI/reviewer-процессов.
