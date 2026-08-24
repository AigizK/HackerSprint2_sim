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

Для следующих срезов уже объявлены остальные события посетителей, экономики и инцидентов. Их обработка в `aggregate`, `state` и `handler` ещё не реализована.

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

Продолжительность создания сервера задаётся событием `InfrastructureConfigured`. После команды добавления сервер имеет статус `provisioning` и не участвует в capacity до `ReadyAt`. Пересекающий `ReadyAt` вызов `AdvanceTime` создаёт `ServerActivated` и `OperationSucceeded`.

Deployments выполняются строго по `Sequence`, по одному за раз. Во время `running` весь backend возвращает `DEPLOYMENT_ERROR`, а активные capacity allocations освобождаются. `AdvanceTime` завершает deployment; только успешная ветка применяет типизированные effects, после чего следующая задача становится `available`. Применённые и неуспешные deployments остаются в каталоге.

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
