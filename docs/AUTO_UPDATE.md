# Automatic updates for the Control Center test server

The Control Center test server may track the latest published stable release automatically.

The canonical updater is `scripts/stable-auto-update.sh`. Install it with:

```sh
sudo bash scripts/install-stable-auto-update.sh
```

The installer enables `control-center-auto-update.timer`, which checks every 10 minutes.

Safety properties:

- only a higher semantic stable version is accepted; automatic downgrade is refused;
- the release archive and published SHA-256 checksum must match;
- PostgreSQL is backed up before an upgrade;
- installation is versioned under `/opt/control-center/<version>` and `current` is switched atomically;
- forward migrations use the existing root-owned environment file and the `control-center` service account;
- live and ready health checks must pass after restart;
- on application startup or health failure, the previous binary/service unit is restored and the database backup is retained for operator recovery;
- `flock` prevents overlapping update attempts.

Useful commands:

```sh
systemctl status control-center-auto-update.timer
systemctl start control-center-auto-update.service
journalctl -u control-center-auto-update.service
```
