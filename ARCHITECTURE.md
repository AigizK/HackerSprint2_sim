# Архитектура SRE-симулятора

## Границы

Каноническая цель описана в `IDEA.md`: uptime не ниже 99% с минимальными
расходами на инфраструктуру. Текущий срез после очистки поддерживает:

- создание мира и продвижение симуляционного времени;
- внутреннюю инициализацию read-only каталога и страницы `product_list` / `product_page`;
- детерминированные visitor journeys, probes, request logs и метрики нагрузки;
- конфигурацию backend, резервирование capacity, отказы при перегрузке и полном диске;
- создание отдельных серверов, provisioning, draining и удаление;
- DDoS как дополнительную нагрузку до истечения атаки либо блокировки firewall;
- накопление серверных расходов без доходов, баланса и банкротства;
- доставку неизменяемых сообщений inbox и ротацию серверных credentials по симуляционному времени;
- управление firewall, backend, БД, дисками и конфигурацией сайта всеми 18 командами панели.

Публичные мутации товаров, покупки, текстовые fixes, очередь deployments и
установка desired backend count удалены вместе с их событиями и сценариями.
Операции `AddServer` / `RemoveServer` остаются внутренними доменными примитивами,
которые вызывают типизированные команды `server.create` / `server.delete`.
Все 18 команд, GET-каталог и источник серверных credentials подключены к API v2.
Time-based uptime учитывает остановку сайта и недоступность firewall, backend и БД.
Error rate не используется как замена uptime или SLO.

## Поток команды

```text
Command
  -> загрузить stream по run_id
  -> replay событий в State
  -> проверить команду через FSM/invariants
  -> получить новые события
  -> append с expected version
  -> обновить projections
```

События являются единственным источником истины. `State` и snapshots не сохраняются: после рестарта состояние всегда восстанавливается полным replay журнала.

Продолжительность создания сервера задаётся событием `InfrastructureConfigured`. После команды добавления сервер имеет статус `provisioning` и не участвует в capacity до `ReadyAt`. Пересекающий `ReadyAt` вызов `AdvanceTime` создаёт `ServerActivated` и `OperationSucceeded`.

Каждая операция сервера записывает `ServerCommandAccepted` с command ID,
operation ID и каноническим payload. Повтор того же ID и payload не повторяет
действие; изменённый payload даёт конфликт. Общего desired count больше нет.

## Параллелизм

Разные `run_id` обрабатываются параллельно. `RunSession` один раз выполняет полный replay при активации run, после чего держит неавторитетный in-memory `State`, последовательно принимает команды и сразу применяет durably appended events. Повторные команды и queries активного run больше не перечитывают journal. После рестарта cache теряется и `RunSession` снова восстанавливает State полным replay; snapshots не используются. Event store дополнительно защищает stream optimistic concurrency через `expectedVersion`.

## Persistence

Локальное постоянное хранилище разделено на каталог и журналы:

- `catalog.db` (SQLite) хранит метаданные `worlds` и `runs`, необходимые для поиска, фильтрации по агенту и идемпотентного `/start`; для отрицательных seed в `definition_json` хранится полное ручное определение мира;
- таблица `run_control_credentials` хранит отдельную случайную пару доступа к панели на run. Она не входит в `RunRecord`, мир, state, journal или audit. Пароль хранится в восстанавливаемом виде внутри приватного каталога, поскольку повторный `/v2/start` должен вернуть его снова; это игровой секрет, доступ к `catalog.db` должен быть ограничен;
- события положительного seed в БД не записываются: генератор воспроизводит их заново, а `schedule_hash` из каталога проверяет идентичность результата;
- у каждого run есть единый append-only journal, содержащий batches доменных `run_events` и пары `agent_request received/completed` в общем порядке;
- активный segment имеет расширение `.log`; при достижении 32 MiB он закрывается, сжимается Zstd в `.log.zst`, после чего открывается следующий segment;
- каждая бинарная запись имеет sequence и CRC32C; оборванный хвост активного segment отбрасывается при восстановлении, повреждение закрытого segment считается ошибкой.

Физический путь run строится по SHA-256 от `run_id`: `runs/<2 hex>/<full hash>/journal/000001.log`. Это не позволяет `run_id` выйти за storage root. Небольшой `EventStore.Append` хранится одной frame, а большой — multipart-последовательностью begin/part/commit; незавершённый multipart batch не replayed. Snapshot-таблиц и snapshot-файлов нет. `CachedEventStore` может ускорять активные runs, но после потери cache полный `State` восстанавливается из journal.

## World generator

Положительный seed вместе с `profile_hash` и `generator_version` детерминированно создаёт immutable `WorldDefinition`. Namespaced random строится через SHA-256 и не зависит от порядка вызовов или goroutines. Из полного годового окна по seed выбирается календарная неделя понедельник–понедельник; поэтому в выборку могут попадать праздники и Black Friday. Генератор создаёт ровно `traffic.target_scheduled_events`: резервирует DDoS, рост БД, десять приращений backend-логов по 1 ГиБ и ротации credentials, затем нормализует часовые, недельные, праздничные и noise-веса и распределяет оставшийся бюджет `VisitorArrived`. До первого DDoS две группы посетителей синхронизируются отдельно: одна требует второй backend, другая при достаточном числе backend насыщает 100 подключений `db.small`. Bootstrap содержит две страницы, тарифную валюту, начальные серверы, готовый read-only каталог и служебное сообщение inbox.

После генерации вычисляется канонический `schedule_hash`. `GetOrCreate` сначала ищет ключ мира в каталоге; если запись уже есть, расписание полностью регенерируется из seed и сверяется с сохранённым hash. Конкурентное создание защищается unique constraint SQLite, поэтому один и тот же ключ сохраняется только один раз.

Используются `world-generator.v12` и профиль `config/world-generation.v2.yaml`.
Старый формат мира не поддерживается. Товарные мини-сценарии, магазинные
credentials, manager permissions, commerce oracle и evaluator прибыли удалены.
Поле `evaluation_json` удалено из новой схемы каталога. Старые тестовые агенты
и калибровка на банкротство также удалены; новый SLO/cost benchmark ещё не реализован.

Для нового симулятора используется пустое хранилище. Автоматической миграции
старого каталога и run events нет; codec явно отклоняет удалённые типы событий.
Операторская debug UI показывает расходы и диагностику без старого score.

## FSM

Run:

```text
not_created --WorldCreated--> running --TimeAdvanced(to EndsAt)--> completed
```

Разрешённые события по состояниям:

| Состояние | Разрешённые события |
|---|---|
| `not_created` | `WorldCreated` |
| `running` | конфигурация мира, read-only каталог, inbox, время, посетители, запросы, серверы, операции, расходы и нагрузочные инциденты |
| `completed` | нет новых команд |

## События

| Event | Назначение |
|---|---|
| `WorldCreated` | Фиксирует run, seed и границы времени |
| `ProductAdded` | Инициализирует read-only товар и вероятность его просмотра |
| `TimeAdvanced` | Фиксирует real/requested/applied duration и новое время |

Остальной каталог разбит по отдельным файлам:

| Файл | Категория событий |
|---|---|
| `events/events.go` | Базовый `Event`, создание мира и каталога, продвижение времени |
| `events/world.go` | Конфигурация страниц и завершение run (`RunEnded`) |
| `events/visitors.go` | Приход посетителя и просмотр страниц |
| `events/requests.go` | Запрос страницы, выделение capacity и HTTP-результат |
| `events/resources.go` | Receipt команды и lifecycle отдельного сервера |
| `events/database.go` | БД, подключения, рост данных/логов, диск, backup/restore и сайт |
| `events/firewall.go` | Правила, решения по запросам и границы доступности firewall |
| `events/operations.go` | Асинхронные operations |
| `events/incidents.go` | Начало и окончание нагрузочной атаки |
| `events/costs.go` | Валюта и накопление серверных расходов |
| `events/inbox.go` | Доставка сообщений |

Общие идентификаторы и enum находятся в `model/types.go`. Пакет `events`
зависит только от `model`, а основной пакет `simulation` зависит от обоих:

```text
model <- events <- simulation
```

Такое направление зависимостей не допускает import cycle.

Вероятности хранятся в PPM: `1_000_000` означает 100%. Деньги хранятся целым числом в минимальных единицах валюты.

## Replay и рабочее состояние

Replay хранит только данные, нужные для следующих решений. Истёкшие capacity
allocations и завершённые детали requests/visitors удаляются из рабочего `State`,
а компактные seen-ID sets сохраняют детерминированную проверку дубликатов. Производный
индекс используемой ёмкости по серверам даёт O(1) для обычной проверки capacity;
он полностью восстанавливается из событий и отдельно не сохраняется.

Сгенерированное расписание после bootstrap неизменяемо, поэтому decision clones
разделяют один backing array. `ScheduleCursor` вместе с binary search выбирает
только ещё не применённый временной интервал без копирования всего расписания.

## Спецификации

DSL в `spec/scenario.go` следует схеме:

```go
s.Given(
    s.World.Created(...),
    s.Product.Added(...),
)

s.When(
    s.User.OpensPage(...),
)

s.Then(
    s.Events.Exactly(...),
    s.State.IsRunning(),
)
```

`Given` напрямую создаёт исторический event stream. `When` проходит полный production-like путь command handler → replay → decision → append. `Then` заново восстанавливает state из event store и проверяет как emitted events, так и результат replay.

## Application layer для HTTP API

HTTP transport в `internal/httpapi` реализован тонким адаптером над
`internal/application`; production-like процесс запуска находится в `cmd/server`
и доступен через `make serve`:

- `StartRunService` создаёт или находит мир, выдаёт криптографически случайный
  base62 `run_id`, довосстанавливает bootstrap и schedule после прерванной записи
  и поддерживает идемпотентный `/v2/start`;
- старт выдаёт сохранённые `control_panel_auth` и полный `commands_markdown`.
  `COMMANDS.md` встроен в бинарник через `go:embed`, не зависит от рабочей
  директории процесса и обновляется при пересборке. Конкурентная выдача доступа
  использует уникальный `run_id` и insert-on-conflict в SQLite;
- `RunManager` лениво открывает `RunSession`, ограничивает число runs в памяти,
  не допускает два одновременных запроса к одному run и разрешает параллельную
  работу разных runs;
- до agent request записывается audit `received`, после него — `completed`; API
  key удаляется из audit headers;
- реальное время синхронизируется перед действием, а `/time/advance` применяет
  один интервал `max(real_elapsed, requested_duration)`; отметка последнего
  реального запроса хранится в SQLite и восстанавливается после рестарта;
- `RunService` предоставляет application-операции для overview, metrics, logs,
  inbox, resources, operations, probes и time advance;
- `Projection` строит ответы из state и records открытой сессии без повторного
  disk replay на каждый GET;
- валидаторы публичных ID и `ClassifyError` отделяют HTTP validation и error
  mapping от доменной модели; секреты `Authorization` и API key редактируются в audit;
- `AuditQuery` читает историю вызовов для операторской debug-страницы, не
  продвигая симуляционное время;
- `DebugQuery` читает журнал без продвижения времени, показывает инфраструктурные расходы, состояние и историю запросов.
- `internal/playerui` обслуживает отдельный ручной интерфейс на `/ui/` в том же
  Go-процессе, не заменяя `/debug/`. Статические HTML/CSS/JS встроены в бинарник;
  браузер вызывает публичные `/v2` endpoints, сериализует запросы одного run и
  хранит Basic/target credentials только в `sessionStorage`. История ручных
  запусков читается через read-only `/ui/api/runs` на базе `DebugQuery`.

Публичные маршруты HTTP и OpenAPI используют `/v2`; алиасов `/v1` нет.
Basic Auth защищает каталог, все 18 команд панели и источник серверных
credentials. Команды, которые обращаются внутрь сервера, дополнительно требуют
актуальный `target_auth`; ротации доставляются через inbox. Остальные пути,
включая `/v2/start`, наблюдаемость, операции и inbox, остаются публичными в
пределах непредсказуемого `run_id`.
