# Control Center test-server automatic update

Desired state: the dedicated Control Center test server checks for the newest published stable release every 10 minutes using `control-center-auto-update.timer` and `scripts/stable-auto-update.sh`.

The updater is fail-closed: checksum validation, PostgreSQL backup, versioned install, migration execution, health checks, overlap lock and application rollback are mandatory.
