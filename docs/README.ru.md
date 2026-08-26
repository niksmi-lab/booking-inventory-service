# Booking Inventory Service

Микросервис атомарного резервирования товарных остатков в PostgreSQL. Поддерживает пополнение остатков, резерв заказа, подтверждение, отмену и автоматический возврат просроченных резервов.

## Быстрый запуск

Требования: Docker и Docker Compose.

1. Создайте локальную конфигурацию:

   ```bash
   cp .env.example .env
   ```

2. Замените значения `POSTGRES_PASSWORD`, `API_KEY` и `ADMIN_API_KEY`. Каждый API-ключ должен содержать минимум 32 символа.

3. Запустите сервис:

   ```bash
   docker-compose up --build
   ```

4. Проверьте состояние:

   ```bash
   curl http://localhost:8080/healthz
   curl http://localhost:8080/readyz
   ```

`healthz` проверяет процесс, `readyz` — доступность PostgreSQL и обязательных таблиц. Метрики `/metrics` защищены admin-токеном.

## API

Все изменяющие endpoints принимают `Content-Type: application/json` и Bearer-токен.

| Метод и путь | Токен | Назначение |
|---|---|---|
| `POST /api/v1/stock/restock` | `ADMIN_API_KEY` | Добавить остаток |
| `POST /api/v1/stock/reserve` | `API_KEY` | Зарезервировать корзину |
| `POST /api/v1/stock/confirm` | `API_KEY` | Подтвердить резерв |
| `POST /api/v1/stock/cancel` | `API_KEY` | Отменить резерв |
| `POST /api/v1/stock/clear` | `API_KEY` | Совместимый alias для `/cancel` |
| `GET /metrics` | `ADMIN_API_KEY` | Prometheus-метрики |

Пример пополнения:

```bash
curl -X POST http://localhost:8080/api/v1/stock/restock \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <ADMIN_API_KEY>' \
  -d '{"items":[{"item_id":"11111111-1111-1111-1111-111111111111","quantity":10}]}'
```

Пример резерва:

```bash
curl -X POST http://localhost:8080/api/v1/stock/reserve \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <API_KEY>' \
  -d '{
    "order_id":"22222222-2222-2222-2222-222222222222",
    "items":[
      {"item_id":"11111111-1111-1111-1111-111111111111","quantity":2}
    ]
  }'
```

Успешная команда возвращает `200 {"status":"success"}`. Ошибка имеет стабильный формат:

```json
{
  "error": {
    "code": "insufficient_stock",
    "message": "one or more products are unavailable",
    "request_id": "..."
  }
}
```

Основные коды: `invalid_request` (400), `unauthorized` (401), `reservation_not_found` (404), `insufficient_stock` / `reservation_expired` / `reservation_conflict` (409), `dependency_timeout` (504), `internal_error` (500).

## Гарантии целостности

- Остаток блокируется транзакцией PostgreSQL перед списанием.
- Товары сортируются по UUID перед блокировкой, что снижает риск deadlock.
- `order_id` является естественным ключом идемпотентности: повтор идентичного reserve не списывает остаток повторно.
- Повтор confirm/cancel безопасен.
- Нельзя подтвердить истёкший или отменённый резерв.
- Cleanup меняет статус на `expired` и сохраняет историю вместо удаления строк.
- Возврат одинакового товара из нескольких резервов предварительно суммируется.
- Один сервисный экземпляр и несколько реплик используют одинаковые гарантии БД.

## Конфигурация

| Переменная | Обязательность / default |
|---|---|
| `DATABASE_URL` | обязательна при запуске бинарника |
| `API_KEY` | обязательна, минимум 32 символа |
| `ADMIN_API_KEY` | обязательна, минимум 32 символа, отличается от `API_KEY` |
| `PORT` | `8080` |
| `RESERVATION_TTL` | `15m` |
| `CLEANUP_INTERVAL` | `1m` |
| `DB_OPERATION_TIMEOUT` | `3s` |
| `SHUTDOWN_TIMEOUT` | `10s` |
| `DB_MAX_CONNECTIONS` | `20` |
| `DB_MIN_CONNECTIONS` | `2` |
| `AUTO_MIGRATE` | `true` |
| `TRUSTED_PROXIES` | пусто; CIDR через запятую |

Для запуска Go-бинарника вне Compose задайте `DATABASE_URL` вручную. Compose собирает его из `POSTGRES_*`.

## Миграции

SQL хранится в `internal/storage/migrations` и встраивается в бинарник. Применённые версии фиксируются в `schema_migrations`. Advisory lock защищает от одновременного запуска одной миграции несколькими репликами.

Миграция `000001` рассчитана на чистую БД. Если таблицы ранее создавались вручную, сначала сделайте backup и подготовьте отдельную миграцию существующей схемы.

## Проверки

```bash
go test ./...
go vet ./...
```

Интеграционный тест требует отдельную тестовую БД и сам очищает таблицы `inventory` и `reservations`:

```bash
TEST_DATABASE_URL='postgres://postgres:password@localhost:5433/stock_test?sslmode=disable' \
  go test ./internal/storage -run TestPostgresRepoIntegration -count=1 -v
```

Никогда не направляйте `TEST_DATABASE_URL` на production-базу.

## Границы ответственности

Bearer-ключи подходят для внутреннего service-to-service API. Для публичного периметра рекомендуется gateway с TLS, rate limiting и OIDC/mTLS. Backup/restore PostgreSQL, алерты Prometheus и управление секретами выполняются инфраструктурой.
