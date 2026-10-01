# Инструкции для работы с проектом

## Технологии

- Backend этого проекта пишется на Go. Не заменяйте Go-сервер другим языком и новую серверную логику реализуйте на Go.
- Версию Go берите из `go.mod` и Dockerfile.
- Frontend находится в `templates/` и `static/`, ресурсы встроены в Go-бинарник.
- Приложение не должно требовать БД, если пользователь явно не изменил архитектурное требование.

## Bitrix24

- Рабочий URL локального приложения: `https://pravv-dev.ru/dashboards/completion-plan`.
- Требуемое право приложения: `CRM`.
- Отдельный webhook не используется: REST вызывается через JS SDK `BX24` в iframe локального приложения.
- Обработчик приложения должен принимать GET и POST.
- REST-методы: `crm.item.fields`, `crm.item.get`, `crm.deal.list`, `crm.status.list`.
- Пользовательские поля контакта:
  - статус: `UF_CRM_1708427400582`;
  - СЗ завершение: `UF_CRM_1708427240655`;
  - запрос суда проверен ОКК: `UF_CRM_1786950686867`;
  - отчёт проверен ОКК: `UF_CRM_1786950695618`.
- Если ID сделки отсутствует, искать сделку по `CONTACT_ID` строго в `CATEGORY_ID = 7` («Завершение процедуры»).
- Не добавлять секреты Bitrix24 в репозиторий.

## Пути и порт

- Порт приложения: `1987`.
- Внешний base path: `/dashboards/completion-plan`.
- Внутренний путь Go-приложения за Caddy `handle_path`: `/completion-plan`.
- Healthcheck: `http://127.0.0.1:1987/completion-plan/healthz`.

## Проверка

- Выполнить `gofmt` для изменённых `.go` файлов.
- Выполнить `go test ./...`, если локальный Go исправен.
- Dockerfile также выполняет тесты во время сборки; успешная серверная сборка подтверждает прохождение тестов в целевой среде.
- Не добавлять в коммит несвязанный каталог `bitrix24-deal-cleaner/` и другие пользовательские изменения.

## Git и деплой

- GitHub: `https://github.com/bulgil/case-completion-dashboard.git`.
- Сервер развёртывания: `root@pravv-dev.ru`.
- Каталог на сервере: `/opt/projects/go-projects/case-completion-dashboard`.
- Сервер обновляется из ветки `main`.
- После реализации и проверки изменений отправить соответствующий коммит в `main`, если пользователь просит обновить или развернуть проект.
- Команда деплоя:

```bash
ssh root@pravv-dev.ru 'cd /opt/projects/go-projects/case-completion-dashboard && git pull --ff-only origin main && docker compose up -d --build --force-recreate && docker compose ps && docker compose exec -T completion-dashboard wget -qO- http://127.0.0.1:1987/completion-plan/healthz'
```

- Успешный healthcheck возвращает `ok`.
- Для диагностики использовать:

```bash
ssh root@pravv-dev.ru 'cd /opt/projects/go-projects/case-completion-dashboard && docker compose logs --tail=200 completion-dashboard'
```

- Не выполнять destructive Git-команды и не перезаписывать несвязанные изменения.
