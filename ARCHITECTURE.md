# Архитектура первого event-sourced среза

## Границы

Первый реализованный срез поддерживает четыре доменные операции:

1. создание мира;
2. добавление товара;
3. покупка товара;
4. продвижение симуляционного времени.

Для следующих срезов уже объявлены события посетителей, страниц, серверной нагрузки, bugs, deployments, operations, экономики и инцидентов. Их обработка в `aggregate`, `state` и `handler` ещё не реализована.

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

События являются единственным источником истины. `State` не сохраняется как authoritative record и всегда может быть восстановлен replay. Snapshot в будущем будет только оптимизацией.

## Параллелизм

Разные `run_id` обрабатываются параллельно. `Engine` создаёт одну goroutine/event loop на активный run и передаёт ей команды через отдельный buffered channel. Event store дополнительно защищает stream optimistic concurrency через `expectedVersion`.

## FSM

Run:

```text
not_created --WorldCreated--> running --TimeAdvanced(to EndsAt)--> completed
```

Разрешённые события по состояниям:

| Состояние | Разрешённые события |
|---|---|
| `not_created` | `WorldCreated` |
| `running` | `ProductAdded`, `ProductPurchased`, `TimeAdvanced` |
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
