# Nexspence — Native Install

Nexspence needs an external **PostgreSQL** (13+). Provision it first, then create a
database and role:

```sql
CREATE DATABASE nexspence;
CREATE ROLE nexspence WITH LOGIN PASSWORD 'changeme';
GRANT ALL PRIVILEGES ON DATABASE nexspence TO nexspence;
```

Before starting the service, edit your `config.yaml` and set at minimum:
`database.dsn`, `auth.jwt_secret` (>= 32 chars), `bootstrap.admin_password`.
The server auto-migrates the schema on first start. Web UI: `http://localhost:8081`.

## Linux (.deb / .rpm)

Prefer the package — it installs the systemd unit and a `nexspence` user for you:

```bash
sudo dpkg -i nexspence_*.deb      # Debian/Ubuntu
sudo rpm -i  nexspence-*.rpm       # RHEL/Fedora/SUSE
sudo nano /etc/nexspence/config.yaml
sudo systemctl enable --now nexspence
```

Or from this archive (manual): copy `nexspence` to `/usr/bin`, `config.yaml.example`
to `/etc/nexspence/config.yaml`, and `packaging/systemd/nexspence.service` to
`/lib/systemd/system/`, then `systemctl daemon-reload`.

## macOS

```bash
sudo cp nexspence /usr/local/bin/
sudo xattr -dr com.apple.quarantine /usr/local/bin/nexspence   # unsigned binary
sudo mkdir -p /usr/local/etc/nexspence /usr/local/var/nexspence /usr/local/var/log
sudo cp config.yaml.example /usr/local/etc/nexspence/config.yaml
sudo nano /usr/local/etc/nexspence/config.yaml
sudo cp packaging/launchd/com.nexspence.server.plist /Library/LaunchDaemons/
sudo launchctl load -w /Library/LaunchDaemons/com.nexspence.server.plist
```

## Windows

Extract the zip, then from an **Administrator** PowerShell in the extracted folder:

```powershell
.\packaging\windows\install-service.ps1
notepad C:\ProgramData\Nexspence\config.yaml
Start-Service nexspence
```

The binary is unsigned — SmartScreen may warn on first run; choose **More info →
Run anyway**.

---
