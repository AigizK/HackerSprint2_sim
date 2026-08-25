# Архитектура первого event-sourced среза

## Границы

Реализованный срез поддерживает следующие доменные операции:

1. создание мира;
2. добавление товара;
3. покупка товара;
4. продвижение симуляционного времени;
5. открытие любой из трёх страниц;
6. активация page bug и получение ответа `500`;
7. отправка правильного или неправильного fix для page bug;
8. конфигурация нагрузки страниц и стартового backend-сервера;
9. резервирование server capacity на время обработки запроса;
10. отказ `SERVER_CAPACITY_EXCEEDED` и освобождение capacity по времени;
11. отложенное добавление backend-сервера через provisioning operation;
12. немедленное удаление свободного сервера;
13. draining и отложенное удаление занятого сервера;
14. последовательный каталог deployments со статусами;
15. downtime и освобождение capacity во время deployment;
16. успешные deployment-эффекты для нагрузки страниц и вероятности багов;
17. failure deployment без применения эффектов;
18. построение site logs из request events.

Также реализованы visitor journeys, почасовое начисление экономики, probes,
операции desired backend count, DDoS с тремя стратегиями завершения и деградация
внешнего провайдера. События инцидентов участвуют в capacity, ответах страниц,
latency, логах и потерянной выручке, а не являются пассивными записями расписания.

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

Deployments выполняются строго по `Sequence`, по одному за раз. Во время `running` весь backend возвращает `DEPLOYMENT_ERROR`, а активные capacity allocations освобождаются. `AdvanceTime` завершает deployment; только успешная ветка применяет типизированные effects, после чего следующая задача становится `available`. Применённые и неуспешные deployments остаются в каталоге.

## Параллелизм

Разные `run_id` обрабатываются параллельно. `RunSession` один раз выполняет полный replay при активации run, после чего держит неавторитетный in-memory `State`, последовательно принимает команды и сразу применяет durably appended events. Повторные команды и queries активного run больше не перечитывают journal. После рестарта cache теряется и `RunSession` снова восстанавливает State полным replay; snapshots не используются. Event store дополнительно защищает stream optimistic concurrency через `expectedVersion`.

## Persistence

Локальное постоянное хранилище разделено на каталог и журналы:

- `catalog.db` (SQLite) хранит метаданные `worlds` и `runs`, необходимые для поиска, фильтрации по агенту и идемпотентного `/start`; для отрицательных seed в `definition_json` хранится полное ручное определение мира;
- события положительного seed в БД не записываются: генератор воспроизводит их заново, а `schedule_hash` из каталога проверяет идентичность результата;
- у каждого run есть единый append-only journal, содержащий batches доменных `run_events` и пары `agent_request received/completed` в общем порядке;
- активный segment имеет расширение `.log`; при достижении 32 MiB он закрывается, сжимается Zstd в `.log.zst`, после чего открывается следующий segment;
- каждая бинарная запись имеет sequence и CRC32C; оборванный хвост активного segment отбрасывается при восстановлении, повреждение закрытого segment считается ошибкой.

Физический путь run строится по SHA-256 от `run_id`: `runs/<2 hex>/<full hash>/journal/000001.log`. Это не позволяет `run_id` выйти за storage root. Небольшой `EventStore.Append` хранится одной frame, а большой — multipart-последовательностью begin/part/commit; незавершённый multipart batch не replayed. Snapshot-таблиц и snapshot-файлов нет. `CachedEventStore` может ускорять активные runs, но после потери cache полный `State` восстанавливается из journal.

## World generator

Положительный seed вместе с `profile_hash` и `generator_version` детерминированно создаёт immutable `WorldDefinition`. Namespaced random строится через SHA-256 и не зависит от порядка вызовов или goroutines. Из полного годового окна по seed выбирается стартовый месяц, а мир длится один календарный месяц; поэтому в выборку могут попадать праздники и Black Friday. Генератор создаёт ровно `traffic.target_scheduled_events`: сначала резервирует позиции DDoS-событий, затем нормализует часовые, недельные, праздничные и noise-веса и распределяет по ним оставшийся бюджет `VisitorArrived`. Bootstrap содержит страницы, экономику, начальные серверы, товары, баги и очередь deployments.

После генерации вычисляется канонический `schedule_hash`. `GetOrCreate` сначала ищет ключ мира в каталоге; если запись уже есть, расписание полностью регенерируется из seed и сверяется с сохранённым hash. Конкурентное создание защищается unique constraint SQLite, поэтому один и тот же ключ сохраняется только один раз.

## World evaluator

`WorldEvaluator` запускается сразу после генератора и строит закрытый benchmark с полным знанием расписания. Версия `world-evaluator.v1` определяет детерминированные покупательские намерения, минимальную инфраструктуру для безошибочного visitor journey, устраняет заранее известные page bugs и не выполняет необязательные действия, уменьшающие итоговый баланс.

Benchmark содержит максимальные выручку и баланс, минимальные расходы на серверы, количество покупок и oracle plan. Минимальное реальное время равно количеству запросов в oracle plan, умноженному на `10s`; длительность симуляционного мира в этот показатель не входит. Evaluation хранится JSON-полем в `worlds`.

## Calibration

`make calibrate` генерирует десять production-размерных месячных миров для seed 1–10 и прогоняет их через FSM с контрольной неэффективной стратегией из `config/calibration.v1.yaml`: шесть backend instances, отсутствие fixes и часовые шаги времени. Каждый мир содержит 100 000 scheduled events при защитном лимите 10 000 000. Gate требует, чтобы ровно один run достиг собственного `world.EndsAt`, а остальные девять завершились раньше из-за отрицательного баланса. Дополнительно выводятся revenue, server cost, баланс, дневной максимум посетителей и максимальное изменение между соседними днями.

## FSM

Run:

```text
not_created --WorldCreated--> running --TimeAdvanced(to EndsAt)--> completed
```

Разрешённые события по состояниям:

| Состояние | Разрешённые события |
|---|---|
| `not_created` | `WorldCreated` |
| `running` | `ProductAdded`, `ProductPurchased`, `TimeAdvanced`, world/resource/request/bug events, `DeploymentDefined`, `DeploymentUnlocked`, `DeploymentStarted`, `DeploymentCompleted`, `DeploymentFailed`, deployment effect events, operation events |
| `completed` | нет новых команд |

## События

| Event | Назначение |
|---|---|
| `WorldCreated` | Фиксирует run, seed и границы времени |
| `ProductAdded` | Создаёт товар вместе с ценой и вероятностями |
| `ProductPurchased` | Фиксирует цену на момент покупки и увеличивает выручку |
| `TimeAdvanced` | Фиксирует real/requested/applied duration и новое время |

Остальной каталог разбит по отдельным файлам:

| Файл | Категория событий |
|---|---|
| `events/events.go` | Базовый `Event` и первые четыре события |
| `events/world.go` | Конфигурация страниц и завершение run (`RunEnded`) |
| `events/visitors.go` | Приход посетителя и прохождение воронки |
| `events/requests.go` | Запрос страницы, выделение capacity и HTTP-результат |
| `events/resources.go` | Масштабирование и lifecycle серверов |
| `events/bugs.go` | Активация бага, его проявление и fix |
| `events/deployments.go` | Очередь deployments и изменение нагрузки страницы |
| `events/operations.go` | Асинхронные operations |
| `events/economy.go` | Расходы и потерянная выручка |
| `events/incidents.go` | DDoS/brute-force и внешние провайдеры |

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
    s.Product.Purchase(...),
)

s.Then(
    s.Events.Exactly(...),
    s.State.Economy(...),
)
```

`Given` напрямую создаёт исторический event stream. `When` проходит полный production-like путь command handler → replay → decision → append. `Then` заново восстанавливает state из event store и проверяет как emitted events, так и результат replay.

## Application layer для HTTP API

HTTP transport в `internal/httpapi` реализован тонким адаптером над
`internal/application`; production-like процесс запуска находится в `cmd/server`
и доступен через `make serve`:

- `StartRunService` создаёт или находит мир, выдаёт криптографически случайный
  base62 `run_id`, довосстанавливает bootstrap и schedule после прерванной записи
  и поддерживает идемпотентный `/v1/start`;
- `RunManager` лениво открывает `RunSession`, ограничивает число runs в памяти,
  не допускает два одновременных запроса к одному run и разрешает параллельную
  работу разных runs;
- до agent request записывается audit `received`, после него — `completed`; API
  key удаляется из audit headers;
- реальное время синхронизируется перед действием, а `/time/advance` применяет
  один интервал `max(real_elapsed, requested_duration)`; отметка последнего
  реального запроса хранится в SQLite и восстанавливается после рестарта;
- `RunService` предоставляет application-операции для overview, metrics, logs,
  resources, fixes, deployments, operations, probes, economy и time advance;
- `Projection` строит ответы из state и records открытой сессии без повторного
  disk replay на каждый GET;
- валидаторы публичных ID и `ClassifyError` отделяют HTTP validation и error
  mapping от доменной модели; игровая версия transport не требует authentication;
- `AuditQuery` читает историю вызовов для операторской debug-страницы, не
  продвигая симуляционное время.
