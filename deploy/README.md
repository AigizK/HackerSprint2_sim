# Deployment

Сервис разворачивается на SSH host `uptick`:

- бинарник и конфигурация: `/opt/hackersprint2-sim`;
- постоянные SQLite/journal данные: `/var/lib/hackersprint2-sim`;
- systemd unit: `hackersprint2-sim.service`;
- HTTP address: `0.0.0.0:8080`;
- процесс работает от системного пользователя `hackersprint2-sim`.

Первичная установка без автоматического запуска:

```bash
make install
make start
```

Обновление уже установленного сервиса:

```bash
make sync
make restart
```

Управление и логи:

```bash
make stop
make start
make restart
make logs
make logs LOG_LINES=500
```

Все команды принимают другой SSH host через `DEPLOY_HOST`, например
`make sync DEPLOY_HOST=root@example.org`. `sync` атомарно заменяет бинарник,
конфигурацию и копию unit-файла, но не перезапускает работающий процесс.
