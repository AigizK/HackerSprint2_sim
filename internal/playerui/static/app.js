(() => {
  "use strict";

  const app = document.querySelector("#app");
  const toastRoot = document.querySelector("#toast-root");
  const dialog = document.querySelector("#confirm-dialog");
  const palette = ["#1464f4", "#ee9b26", "#087a55", "#c72b3d"];
  const errorCodes = [
    "SERVER_CAPACITY_EXCEEDED",
    "DB_CONNECTION_LIMIT_EXCEEDED",
    "DISK_FULL",
    "SITE_UNAVAILABLE",
    "DB_UNAVAILABLE",
    "FIREWALL_DENIED",
  ];
  const commandGroups = {
    all: "Все",
    firewall: "Firewall",
    server: "Серверы",
    database: "База данных",
    site: "Сайт",
    disk: "Диски",
  };

  let runQueue = Promise.resolve();
  let operationTimer = 0;
  let runtime = freshRuntime();

  function freshRuntime() {
    return {
      runId: "",
      overview: null,
      resources: null,
      metrics: null,
      inbox: null,
      catalog: null,
      catalogError: "",
      auth: { username: "", password: "" },
      credentials: {},
      activeTargetServer: "",
      operations: [],
      console: [],
      metricRange: "6h",
      stopOnLogError: false,
      commandGroup: "all",
      selectedCommand: "",
      commandMode: "visual",
      logFilters: { presence: "all", error: "", from: "", to: "", page: "", limit: "200" },
      logCursors: [""],
      logCursorIndex: 0,
      logs: null,
    };
  }

  function escapeHTML(value) {
    return String(value ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#039;");
  }

  function requestId(prefix) {
    const token = crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
    return `${prefix}-${token}`.slice(0, 128);
  }

  function basicHeader(auth = runtime.auth) {
    if (!auth.username || !auth.password) return "";
    return `Basic ${btoa(`${auth.username}:${auth.password}`)}`;
  }

  async function api(path, options = {}) {
    const headers = new Headers(options.headers || {});
    if (options.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    if (options.basic) {
      const authorization = basicHeader();
      if (authorization) headers.set("Authorization", authorization);
    }
    const response = await fetch(path, { ...options, headers, cache: "no-store" });
    const raw = await response.text();
    let payload = null;
    if (raw) {
      try { payload = JSON.parse(raw); } catch { payload = { message: raw.trim() }; }
    }
    if (!response.ok) {
      const error = new Error(payload?.message || `${response.status} ${response.statusText}`);
      error.status = response.status;
      error.code = payload?.error || "HTTP_ERROR";
      error.payload = payload;
      throw error;
    }
    return payload;
  }

  function runApi(path, options) {
    const task = runQueue.then(() => api(path, options));
    runQueue = task.catch(() => undefined);
    return task;
  }

  function setNavigation(active) {
    document.querySelectorAll("[data-nav]").forEach((link) => link.classList.toggle("active", link.dataset.nav === active));
  }

  function navigate(path) {
    if (location.pathname !== path) history.pushState({}, "", path);
    route();
  }

  document.addEventListener("click", (event) => {
    const link = event.target.closest("a[data-link]");
    if (!link || link.origin !== location.origin || event.metaKey || event.ctrlKey || event.shiftKey) return;
    event.preventDefault();
    navigate(link.pathname);
  });
  window.addEventListener("popstate", route);

  async function route() {
    clearTimeout(operationTimer);
    operationTimer = 0;
    const path = location.pathname.replace(/\/+$/, "") || "/ui";
    const logsMatch = path.match(/^\/ui\/runs\/([A-Za-z0-9]{20,64})\/logs$/);
    const runMatch = path.match(/^\/ui\/runs\/([A-Za-z0-9]{20,64})$/);
    if (logsMatch) {
      setNavigation("");
      await loadLogsPage(logsMatch[1]);
      return;
    }
    if (runMatch) {
      setNavigation("");
      await loadWorkspace(runMatch[1]);
      return;
    }
    if (path === "/ui/history") {
      setNavigation("history");
      await renderHistoryPage();
      return;
    }
    setNavigation("start");
    await renderStartPage();
  }

  function loading(label = "Загружаем данные…") {
    app.innerHTML = `<div class="page-loading"><span class="loader"></span>${escapeHTML(label)}</div>`;
  }

  function renderError(error, retryPath = location.pathname) {
    app.innerHTML = `<section class="error-state">
      <div class="eyebrow">Не удалось загрузить экран</div>
      <h1>Симулятор не ответил</h1>
      <p class="muted">Проверьте состояние процесса и повторите запрос.</p>
      <code>${escapeHTML(error.code || "ERROR")}: ${escapeHTML(error.message)}</code>
      <button class="button" id="retry-page">Повторить</button>
    </section>`;
    document.querySelector("#retry-page")?.addEventListener("click", () => navigate(retryPath));
  }

  function toast(title, detail = "", kind = "") {
    const item = document.createElement("div");
    item.className = `toast ${kind}`;
    item.innerHTML = `<strong>${escapeHTML(title)}</strong>${detail ? `<span>${escapeHTML(detail)}</span>` : ""}`;
    toastRoot.append(item);
    setTimeout(() => item.remove(), 4200);
  }

  async function confirmAction(title, copy) {
    document.querySelector("#dialog-title").textContent = title;
    document.querySelector("#dialog-copy").textContent = copy;
    dialog.returnValue = "cancel";
    dialog.showModal();
    await new Promise((resolve) => dialog.addEventListener("close", resolve, { once: true }));
    return dialog.returnValue === "confirm";
  }

  async function historyRows() {
    return api("/ui/api/runs?limit=100");
  }

  async function renderStartPage() {
    loading("Готовим стартовую панель…");
    let history = { runs: [] };
    try { history = await historyRows(); } catch (error) { toast("История недоступна", error.message, "error"); }
    app.innerHTML = `<section class="start-layout">
      <div class="start-stage">
        <div class="start-copy">
          <div>
            <div class="eyebrow">SRE simulation · one week</div>
            <h1>Примите инфраструктуру.<br>Удержите сервис в строю.</h1>
            <p>Недельная детерминированная симуляция: нагрузка, сбои, атаки, ротация доступов и стоимость каждого решения.</p>
          </div>
          <div class="objective-line">
            <strong>Uptime ≥ 99%</strong>
            <span>Сначала выполните SLO,<br>затем минимизируйте расходы.</span>
          </div>
        </div>
        <form class="start-form" id="start-form">
          <div class="eyebrow">Новый запуск</div>
          <h2>Выберите seed мира</h2>
          <label class="field"><span>Seed</span>
            <div class="seed-row">
              <input id="seed" name="seed" type="number" step="1" required value="${randomSeed()}">
              <button class="icon-button" id="random-seed" type="button" title="Случайный seed">↻</button>
            </div>
            <small class="field-hint">Положительный seed создаёт воспроизводимый мир.</small>
          </label>
          <button class="button" type="submit">СТАРТ <span aria-hidden="true">→</span></button>
          <div class="start-error" id="start-error"></div>
        </form>
      </div>
      <aside class="brief-panel">
        <div class="eyebrow">Правила смены</div>
        <h2>Решения видны сразу</h2>
        <ol>
          <li><b>01</b><div><strong>Следите за сигналами</strong><span>Inbox, нагрузка, latency, ошибки и заполнение дисков.</span></div></li>
          <li><b>02</b><div><strong>Управляйте инфраструктурой</strong><span>18 типизированных команд вместо shell.</span></div></li>
          <li><b>03</b><div><strong>Платите за каждый час</strong><span>Лишние мощности сохраняют uptime, но ухудшают итог.</span></div></li>
        </ol>
      </aside>
    </section>
    <section class="history-section">
      <div class="section-heading"><div><h2>Последние запуски</h2><p>Ручные runs этого интерфейса.</p></div><a href="/ui/history" class="button secondary compact" data-link>Вся история</a></div>
      ${historyTable(history.runs.slice(0, 8))}
    </section>`;
    document.querySelector("#random-seed").addEventListener("click", () => { document.querySelector("#seed").value = randomSeed(); });
    document.querySelector("#start-form").addEventListener("submit", startRun);
  }

  function randomSeed() {
    const values = new Uint32Array(1);
    crypto.getRandomValues(values);
    return String((values[0] % 2147483646) + 1);
  }

  async function startRun(event) {
    event.preventDefault();
    const form = event.currentTarget;
    const button = form.querySelector("button[type=submit]");
    const errorBox = document.querySelector("#start-error");
    const seed = Number(document.querySelector("#seed").value);
    if (!Number.isSafeInteger(seed) || seed === 0) {
      errorBox.textContent = "Seed должен быть ненулевым целым числом.";
      return;
    }
    button.disabled = true;
    button.innerHTML = `<span class="loader"></span> СОЗДАЁМ МИР`;
    errorBox.textContent = "";
    try {
      const result = await api("/v2/start", {
        method: "POST",
        body: JSON.stringify({ seed, agent_id: "human-ui", agent_version: "1.0", request_id: requestId("start") }),
      });
      sessionStorage.setItem(authKey(result.run_id), JSON.stringify(result.control_panel_auth));
      sessionStorage.setItem("uptick.activeRun", result.run_id);
      toast("Run создан", `Seed ${result.seed}`);
      navigate(`/ui/runs/${result.run_id}`);
    } catch (error) {
      errorBox.textContent = `${error.code}: ${error.message}`;
      button.disabled = false;
      button.textContent = "СТАРТ →";
    }
  }

  async function renderHistoryPage() {
    loading("Читаем историю runs…");
    try {
      const history = await historyRows();
      const completed = history.runs.filter((run) => run.status === "completed").length;
      app.innerHTML = `<div class="section-heading">
        <div><div class="eyebrow">Run archive</div><h1>История запусков</h1><p>${history.runs.length} runs · ${completed} завершено</p></div>
        <a href="/ui/" class="button" data-link>Новый run</a>
      </div>${historyTable(history.runs, true)}`;
    } catch (error) { renderError(error); }
  }

  function historyTable(runs, full = false) {
    if (!runs.length) return `<div class="panel table-empty">Запусков пока нет. Создайте первый мир выше.</div>`;
    const rows = runs.map((run) => {
      const broken = run.load_error;
      const uptime = run.uptime_ratio == null ? "—" : percent(run.uptime_ratio, 2);
      return `<tr>
        <td><div class="primary-cell mono truncate" title="${escapeHTML(run.run_id)}">${escapeHTML(run.run_id)}</div><small class="muted">seed ${run.seed}</small></td>
        <td><span class="badge ${statusClass(run.status)}">${escapeHTML(run.status)}</span></td>
        <td><span class="badge ${statusClass(run.site_status)}">${escapeHTML(run.site_status || "unknown")}</span></td>
        <td class="mono nowrap">${formatDateTime(run.simulation_time)}</td>
        <td class="mono">${uptime}</td>
        <td class="mono">${formatMoney(run.total_cost_minor, run.currency)}</td>
        ${full ? `<td class="mono">${formatCompact(run.event_count)}</td>` : ""}
        <td>${broken ? `<span class="log-code">${escapeHTML(broken)}</span>` : `<a class="button secondary compact" href="/ui/runs/${run.run_id}" data-link>${run.status === "completed" ? "Открыть отчёт" : "Продолжить"}</a>`}</td>
      </tr>`;
    }).join("");
    return `<div class="panel table-scroll"><table class="data-table"><thead><tr><th>Run</th><th>Состояние</th><th>Сервис</th><th>Сим. время</th><th>Uptime</th><th>Стоимость</th>${full ? "<th>События</th>" : ""}<th></th></tr></thead><tbody>${rows}</tbody></table></div>`;
  }

  function authKey(runId) { return `uptick.auth.${runId}`; }
  function credentialsKey(runId) { return `uptick.targetCredentials.${runId}`; }
  function operationsKey(runId) { return `uptick.operations.${runId}`; }
  function loadJSON(key, fallback) {
    try { return JSON.parse(sessionStorage.getItem(key)) ?? fallback; } catch { return fallback; }
  }

  async function loadWorkspace(runId) {
    loading("Собираем состояние инфраструктуры…");
    runtime = freshRuntime();
    runtime.runId = runId;
    runtime.auth = loadJSON(authKey(runId), runtime.auth);
    runtime.credentials = loadJSON(credentialsKey(runId), {});
    runtime.operations = loadJSON(operationsKey(runId), []).map((id) => ({ operation_id: id, status: "queued", progress: 0, command: "operation" }));
    try {
      runtime.overview = await runApi(`/v2/runs/${runId}/overview`);
      runtime.resources = await runApi(`/v2/runs/${runId}/resources`);
      runtime.inbox = await runApi(`/v2/runs/${runId}/inbox?limit=200`);
      runtime.metrics = await fetchMetrics();
      await loadCatalog();
      renderWorkspace();
      pollOperations();
    } catch (error) { renderError(error); }
  }

  async function loadCatalog() {
    runtime.catalog = null;
    runtime.catalogError = "";
    if (!basicHeader()) {
      runtime.catalogError = "Введите логин и пароль панели в верхней строке.";
      return;
    }
    try {
      const result = await runApi(`/v2/runs/${runtime.runId}/control/commands`, { basic: true });
      runtime.catalog = result.commands || [];
      if (!runtime.selectedCommand && runtime.catalog.length) runtime.selectedCommand = runtime.catalog[0].command;
    } catch (error) {
      runtime.catalogError = `${error.code}: ${error.message}`;
    }
  }

  function metricWindow() {
    const current = new Date(runtime.overview.clock.simulation_time);
    const starts = {
      "1h": new Date(current.getTime() - 3600e3),
      "6h": new Date(current.getTime() - 6 * 3600e3),
      "24h": new Date(current.getTime() - 24 * 3600e3),
      week: new Date(new Date(runtime.overview.clock.simulation_ends_at).getTime() - 7 * 24 * 3600e3),
    };
    const seconds = { "1h": 60, "6h": 300, "24h": 1200, week: 7200 };
    return { from: starts[runtime.metricRange], to: current, step: seconds[runtime.metricRange] };
  }

  async function fetchMetrics() {
    const window = metricWindow();
    const names = [
      "used_load_units", "capacity_units", "capacity_utilization", "latency_p50_ms", "latency_p95_ms", "error_rate",
      "database_active_connections", "database_connection_limit", "disk_database_bytes", "disk_logs_bytes", "disk_free_bytes",
    ].join(",");
    const query = new URLSearchParams({ from: window.from.toISOString(), to: window.to.toISOString(), step_seconds: String(window.step), names });
    return runApi(`/v2/runs/${runtime.runId}/metrics?${query}`);
  }

  function renderWorkspace() {
    const overview = runtime.overview;
    const metrics = runtime.metrics.current;
    const readonly = overview.status !== "running";
    const uptime = overview.availability.uptime_ratio;
    const backends = runtime.resources.servers.filter((server) => server.role === "backend");
    const databases = runtime.resources.servers.filter((server) => server.role === "database");
    const connected = metrics.database_active_connections;
    const connectionLimit = metrics.database_connection_limit;
    app.innerHTML = `<section class="workspace-head">
      <div>
        <div class="breadcrumb"><a href="/ui/history" data-link>История</a><span>/</span><span>run</span></div>
        <div class="run-title"><h1 title="${escapeHTML(runtime.runId)}">${escapeHTML(runtime.runId)}</h1><span class="badge ${statusClass(overview.status)}">${escapeHTML(overview.status)}</span></div>
      </div>
      <div class="workspace-actions"><a class="button secondary compact" href="/ui/runs/${runtime.runId}/logs" data-link>Открыть логи</a><div class="sim-clock"><time>${formatDateTime(overview.clock.simulation_time)}</time><span>${formatDuration(overview.clock.remaining_seconds)} до конца недели</span></div></div>
    </section>
    ${readonly ? `<div class="readonly-banner"><strong>Run завершён — доступен режим просмотра.</strong><span>Команды и прокрутка времени отключены.</span></div>` : ""}
    ${renderControlStrip(readonly)}
    <section class="status-ribbon">
      <div class="status-cell primary"><div class="label">Сервис</div><div class="value"><span class="badge ${statusClass(overview.site_status)}">${escapeHTML(overview.site_status)}</span></div><div class="sub">time-based availability</div></div>
      <div class="status-cell"><div class="label">Uptime</div><div class="value">${uptime == null ? "—" : percent(uptime, 3)}</div><div class="sub">цель ≥ 99.000%</div></div>
      <div class="status-cell"><div class="label">Downtime</div><div class="value">${formatDuration(overview.availability.downtime_seconds)}</div><div class="sub">из ${formatDuration(overview.availability.observed_seconds)}</div></div>
      <div class="status-cell"><div class="label">Backend capacity</div><div class="value">${formatCompact(runtime.resources.used_load_units)} / ${formatCompact(runtime.resources.total_capacity_units)}</div><div class="sub">${backends.length} backend · ${percent(safeRatio(runtime.resources.used_load_units, runtime.resources.total_capacity_units), 1)} занято</div></div>
      <div class="status-cell"><div class="label">DB connections</div><div class="value">${connected} / ${connectionLimit}</div><div class="sub">${databases.length} database server</div></div>
      <div class="status-cell"><div class="label">Стоимость</div><div class="value">${formatMoney(overview.costs.total_cost_minor, overview.costs.currency)}</div><div class="sub">${formatMoney(overview.costs.current_cost_per_hour_minor, overview.costs.currency)} / час</div></div>
    </section>
    <div class="workspace-grid">
      <div class="main-stack">
        ${renderResources(readonly)}
        ${renderCharts(metrics)}
      </div>
      <aside class="side-stack">
        ${renderInbox()}
        ${renderOperations()}
      </aside>
    </div>
    ${renderCommandStudio(readonly)}`;
    bindWorkspaceEvents(readonly);
  }

  function renderControlStrip(readonly) {
    const auth = runtime.auth;
    return `<section class="control-strip">
      <div class="time-controls">
        <span class="control-label">Промотать</span>
        ${[[300,"+5 мин"],[3600,"+1 час"],[21600,"+6 часов"],[86400,"+1 день"]].map(([seconds,label]) => `<button class="button secondary compact" data-advance="${seconds}" ${readonly ? "disabled" : ""}>${label}</button>`).join("")}
        <div class="custom-duration"><input id="duration-value" type="number" min="1" value="30" ${readonly ? "disabled" : ""}><select id="duration-unit" ${readonly ? "disabled" : ""}><option value="60">минут</option><option value="3600">часов</option><option value="86400">дней</option></select><button class="button secondary compact" id="advance-custom" ${readonly ? "disabled" : ""}>Применить</button></div>
        <label class="alert-stop-control" title="Остановить прокрутку на первой новой ошибке в логе"><input id="stop-on-log-error" type="checkbox" ${runtime.stopOnLogError ? "checked" : ""} ${readonly ? "disabled" : ""}><span>до 1 ошибки</span></label>
        <button class="button danger compact" id="finish-run" ${readonly ? "disabled" : ""}>Завершить run</button>
      </div>
      <div class="auth-stack">
        <div class="auth-controls">
          <span class="control-label">Basic Auth</span>
          <input id="panel-username" type="text" autocomplete="off" placeholder="login" value="${escapeHTML(auth.username)}" ${readonly ? "disabled" : ""}>
          <div class="password-wrap"><input id="panel-password" type="password" autocomplete="off" placeholder="password" value="${escapeHTML(auth.password)}" ${readonly ? "disabled" : ""}><button class="password-toggle" id="toggle-password" type="button" aria-label="Показать пароль">◉</button></div>
          <button class="button secondary compact" id="save-auth" ${readonly ? "disabled" : ""}>Подключить</button>
        </div>
        <span class="auth-note">Уровень 1 · доступ к Control API, получен при START и хранится до закрытия вкладки</span>
      </div>
    </section>`;
  }

  function renderResources(readonly) {
    const rows = runtime.resources.servers.map((server) => {
      const util = safeRatio(server.used_load_units, server.capacity_units);
      const disk = server.disk;
      const diskUsed = safeRatio(disk.used_bytes, disk.total_bytes);
      const meterClass = util >= 1 ? "bad" : util >= .75 ? "warn" : "";
      const diskClass = diskUsed >= .95 ? "bad" : diskUsed >= .8 ? "warn" : "";
      const stored = runtime.credentials[server.server_id];
      const hasCurrentCredential = stored?.credential_id === server.credential_id;
      return `<tr>
        <td><div class="resource-name"><strong>${escapeHTML(server.name)}</strong><code>${escapeHTML(server.server_id)}</code></div></td>
        <td><span class="role">${escapeHTML(server.role)}</span><br><span class="muted mono">${escapeHTML(server.instance_type)}</span></td>
        <td><span class="badge ${statusClass(server.status)}">${escapeHTML(server.status)}</span></td>
        <td><div class="meter ${meterClass}"><span style="width:${Math.min(100, util * 100)}%"></span></div><div class="meter-copy">${formatCompact(server.used_load_units)} / ${formatCompact(server.capacity_units)} ${server.role === "database" ? "conn" : "units"}</div></td>
        <td><div class="disk-meter ${diskClass}" title="${percent(diskUsed, 1)} занято"><span class="system" style="width:${safeRatio(disk.system_bytes, disk.total_bytes)*100}%"></span><span class="database" style="width:${safeRatio(disk.database_bytes, disk.total_bytes)*100}%"></span><span class="logs" style="width:${safeRatio(disk.logs_bytes, disk.total_bytes)*100}%"></span></div>
          <div class="disk-legend"><span><i style="background:#8190a2"></i>sys ${formatBytes(disk.system_bytes)}</span><span><i style="background:#1464f4"></i>data ${formatBytes(disk.database_bytes)}</span><span><i style="background:#ee9b26"></i>logs ${formatBytes(disk.logs_bytes)}</span><span>free ${formatBytes(disk.free_bytes)}</span></div></td>
        <td class="mono nowrap">${formatMoney(server.cost_per_hour_minor, runtime.overview.costs.currency)}/h</td>
        <td><div class="server-actions"><button class="button secondary compact" data-credential-server="${escapeHTML(server.server_id)}" data-credential-id="${escapeHTML(server.credential_id)}" ${readonly || !server.credential_id ? "disabled" : ""}>${hasCurrentCredential ? "Подставить доступ" : "Получить доступ"}</button></div></td>
      </tr>`;
    }).join("");
    return `<section class="panel">
      <header class="panel-header"><div><h2>Инфраструктура</h2><p>Capacity, подключения и физическое заполнение дисков.</p></div><div class="resource-summary"><span><strong>${runtime.resources.servers.length}</strong> серверов</span><span><strong>${formatMoney(runtime.resources.total_cost_per_hour_minor, runtime.overview.costs.currency)}</strong> / час</span><button class="button ghost compact" id="refresh-resources">↻ Обновить</button></div></header>
      <div class="table-scroll"><table class="data-table"><thead><tr><th>Сервер</th><th>Роль / тип</th><th>Статус</th><th>Нагрузка</th><th>Диск: system / data / logs / free</th><th>Цена</th><th>Credentials</th></tr></thead><tbody>${rows || `<tr><td colspan="7" class="table-empty">Серверов нет</td></tr>`}</tbody></table></div>
    </section>`;
  }

  function renderCharts(snapshot) {
    const ranges = { "1h": "1ч", "6h": "6ч", "24h": "24ч", week: "Неделя" };
    return `<section class="panel">
      <header class="panel-header"><div><h2>Сигналы за период</h2><p>Нагрузка и качество сервиса в симуляционном времени.</p></div><div class="range-switch">${Object.entries(ranges).map(([key,label]) => `<button data-metric-range="${key}" class="${runtime.metricRange === key ? "active" : ""}">${label}</button>`).join("")}</div></header>
      <div class="charts-grid">
        ${chartBlock("Backend load", `${percent(snapshot.capacity_utilization, 1)} сейчас`, lineChart([
          { name: "used_load_units", label: "used", color: palette[0] }, { name: "capacity_units", label: "capacity", color: "#8190a2" },
        ], (v) => formatCompact(v)))}
        ${chartBlock("Latency", `p95 ${formatNumber(snapshot.latency_p95_ms)} ms`, lineChart([
          { name: "latency_p50_ms", label: "p50", color: palette[2], average: true }, { name: "latency_p95_ms", label: "p95", color: palette[1], average: true },
        ], (v) => `${formatCompact(v)} ms`))}
        ${chartBlock("Error rate", percent(snapshot.error_rate, 2), lineChart([
          { name: "error_rate", label: "errors", color: palette[3], average: true, transform: (v) => v * 100 },
        ], (v) => `${formatNumber(v)}%`))}
        ${chartBlock("DB connections", `${snapshot.database_active_connections} / ${snapshot.database_connection_limit}`, lineChart([
          { name: "database_active_connections", label: "active", color: palette[0] }, { name: "database_connection_limit", label: "limit", color: "#8190a2" },
        ], (v) => formatCompact(v)))}
        ${chartBlock("Disk · data / logs / free", formatBytes(snapshot.disk_total_bytes), lineChart([
          { name: "disk_database_bytes", label: "data", color: palette[0], transform: toGiB }, { name: "disk_logs_bytes", label: "logs", color: palette[1], transform: toGiB }, { name: "disk_free_bytes", label: "free", color: "#8190a2", transform: toGiB },
        ], (v) => `${formatNumber(v)} GiB`), "wide")}
      </div>
    </section>`;
  }

  function chartBlock(title, value, chart, extra = "") {
    return `<div class="chart-block ${extra}"><div class="chart-head"><h3>${escapeHTML(title)}</h3><div class="chart-value">${escapeHTML(value)}</div></div><div class="chart">${chart}</div></div>`;
  }

  function lineChart(definitions, formatY) {
    const all = definitions.map((definition) => ({ ...definition, points: metricPoints(definition) })).filter((series) => series.points.length);
    if (!all.length) return `<div class="chart-empty">Нет точек за выбранный период</div>`;
    const timestamps = all.flatMap((series) => series.points.map((point) => point.time));
    const values = all.flatMap((series) => series.points.map((point) => point.value));
    const minX = Math.min(...timestamps);
    const maxX = Math.max(...timestamps);
    const maxY = Math.max(1, ...values) * 1.08;
    const width = 520, height = 142, left = 38, right = 8, top = 8, bottom = 22;
    const plotW = width - left - right, plotH = height - top - bottom;
    const x = (value) => left + (maxX === minX ? plotW / 2 : ((value - minX) / (maxX - minX)) * plotW);
    const y = (value) => top + plotH - (value / maxY) * plotH;
    const grid = [0, .5, 1].map((ratio) => {
      const gy = top + plotH * ratio;
      return `<line class="chart-grid-line" x1="${left}" x2="${width-right}" y1="${gy}" y2="${gy}"/><text class="chart-axis-label" x="0" y="${gy+3}">${escapeHTML(formatY(maxY * (1-ratio)))}</text>`;
    }).join("");
    const paths = all.map((series) => {
      const d = series.points.map((point, index) => `${index ? "L" : "M"}${x(point.time).toFixed(1)},${y(point.value).toFixed(1)}`).join(" ");
      return `<path class="chart-line" d="${d}" stroke="${series.color}"/>`;
    }).join("");
    const legend = all.map((series) => `<span><i style="background:${series.color}"></i>${escapeHTML(series.label)}</span>`).join("");
    return `<svg viewBox="0 0 ${width} ${height}" role="img" aria-label="График ${escapeHTML(definitions.map((item) => item.label).join(", "))}">${grid}${paths}<text class="chart-axis-label" x="${left}" y="${height-3}">${formatShortTime(minX)}</text><text class="chart-axis-label" text-anchor="end" x="${width-right}" y="${height-3}">${formatShortTime(maxX)}</text></svg><div class="chart-legend">${legend}</div>`;
  }

  function metricPoints(definition) {
    const buckets = new Map();
    runtime.metrics.series.filter((point) => point.name === definition.name).forEach((point) => {
      const key = new Date(point.timestamp).getTime();
      const value = definition.transform ? definition.transform(point.value) : point.value;
      const bucket = buckets.get(key) || [];
      bucket.push(value);
      buckets.set(key, bucket);
    });
    return [...buckets.entries()].sort((a,b) => a[0]-b[0]).map(([time, values]) => ({
      time,
      value: definition.average ? values.reduce((sum, value) => sum + value, 0) / values.length : values.reduce((sum, value) => sum + value, 0),
    }));
  }

  function renderInbox() {
    const messages = runtime.inbox.messages || [];
    const newestFirst = messages.slice().sort((a, b) => new Date(b.sent_at) - new Date(a.sent_at));
    const items = newestFirst.map((message, index) => `<button class="inbox-item ${index === 0 ? "active" : ""}" data-message-id="${escapeHTML(message.message_id)}"><div class="inbox-meta"><span>${escapeHTML(message.sender_email)}</span><time>${formatShortDate(message.sent_at)}</time></div><div class="inbox-subject">${escapeHTML(message.subject)}</div></button>`).join("");
    const active = newestFirst[0];
    return `<section class="panel"><header class="panel-header"><div><h2>Inbox</h2><p>${messages.length} доставленных сообщений</p></div><button class="button ghost compact" id="refresh-inbox">↻</button></header><div class="inbox-list">${items || `<div class="table-empty">Новых сообщений нет</div>`}</div>${active ? messageView(active) : ""}</section>`;
  }

  function messageView(message) {
    return `<article class="message-view"><div class="sender">${escapeHTML(message.sender_email)} · ${formatDateTime(message.sent_at)}</div><h3>${escapeHTML(message.subject)}</h3><p>${linkifyCredentialIDs(message.description)}</p></article>`;
  }

  function linkifyCredentialIDs(value) {
    return escapeHTML(value).replace(/\b([A-Za-z0-9][A-Za-z0-9._:-]{8,127})\b/g, (match) => match.includes("credential") ? `<code>${match}</code>` : match);
  }

  function renderOperations() {
    const operations = runtime.operations;
    const items = operations.slice().reverse().map((operation) => `<div class="operation-item" data-operation-id="${escapeHTML(operation.operation_id)}"><div class="operation-top"><span class="operation-command">${escapeHTML(operation.command || operation.type)}</span><span class="badge ${statusClass(operation.status)}">${escapeHTML(operation.status)}</span></div><div class="operation-progress"><span style="width:${Math.max(2, Math.min(100, (operation.progress || 0) * 100))}%"></span></div><div class="operation-detail"><span>${escapeHTML(operation.operation_id)}</span><span>${percent(operation.progress || 0, 0)}</span></div></div>`).join("");
    return `<section class="panel" id="operations-panel"><header class="panel-header"><div><h2>Операции</h2><p>Автоматическое отслеживание прогресса</p></div><button class="button ghost compact" id="refresh-operations">↻</button></header><div class="operation-list">${items || `<div class="table-empty">Длительных операций нет</div>`}</div></section>`;
  }

  function renderCommandStudio(readonly) {
    if (readonly) return "";
    if (!runtime.catalog) {
      return `<section class="panel command-studio"><header class="panel-header"><div><h2>Командный центр</h2><p>18 типизированных инфраструктурных действий.</p></div></header><div class="catalog-locked"><strong>Каталог команд закрыт</strong><span>${escapeHTML(runtime.catalogError)}</span></div></section>`;
    }
    const filtered = runtime.catalog.filter((definition) => runtime.commandGroup === "all" || definition.command.startsWith(`${runtime.commandGroup}.`));
    if (!filtered.some((item) => item.command === runtime.selectedCommand)) runtime.selectedCommand = filtered[0]?.command || runtime.catalog[0]?.command;
    const definition = runtime.catalog.find((item) => item.command === runtime.selectedCommand) || runtime.catalog[0];
    const categories = Object.entries(commandGroups).map(([key,label]) => `<button data-command-group="${key}" class="${runtime.commandGroup === key ? "active" : ""}">${label}</button>`).join("");
    const options = filtered.map((item) => `<button class="command-option ${item.command === definition.command ? "active" : ""}" data-command="${escapeHTML(item.command)}"><code>${escapeHTML(item.command)}</code><span>${escapeHTML(item.description)}</span></button>`).join("");
    const raw = defaultRawEnvelope(definition);
    return `<section class="panel command-studio">
      <header class="panel-header"><div><h2>Командный центр</h2><p>Формы строятся из живого каталога API.</p></div><div class="mode-switch"><button data-command-mode="visual" class="${runtime.commandMode === "visual" ? "active" : ""}">Форма</button><button data-command-mode="raw" class="${runtime.commandMode === "raw" ? "active" : ""}">Raw JSON</button></div></header>
      <div class="command-shell">
        <aside class="command-nav"><div class="command-categories"><div class="category-switch">${categories}</div></div><div class="command-list">${options}</div></aside>
        <form class="command-form" id="command-form">
          <h2 class="mono">${escapeHTML(definition.command)}</h2><p class="command-description">${escapeHTML(definition.description)} · ${definition.execution === "asynchronous" ? "длительная операция" : "синхронно"}</p>
          ${runtime.commandMode === "visual" ? `<div class="schema-fields">${renderSchemaFields(definition.params_schema, "")}</div>${definition.target_auth_required ? renderTargetAuth() : ""}` : `<div class="raw-auth-note"><strong>Authorization header</strong> берётся из Basic Auth сверху. Поле <code>target_auth</code> — отдельная пара credentials выбранного сервера.</div><label class="field"><span>Request envelope</span><textarea class="raw-editor" id="raw-command" spellcheck="false">${escapeHTML(JSON.stringify(raw, null, 2))}</textarea></label>`}
          <div class="command-actions"><span class="request-id">request_id создаётся автоматически</span><button class="button" type="submit">Выполнить команду →</button></div>
        </form>
        ${renderConsole()}
      </div>
    </section>`;
  }

  function renderSchemaFields(schema, prefix) {
    const properties = schema?.properties || {};
    const required = new Set(schema?.required || []);
    if (!Object.keys(properties).length) return `<div class="muted wide">У команды нет параметров.</div>`;
    return Object.entries(properties).map(([name, property]) => {
      const path = prefix ? `${prefix}.${name}` : name;
      if (property.type === "object") {
        return `<fieldset class="schema-group"><legend>${escapeHTML(labelFor(name))}${required.has(name) ? " *" : ""}</legend><div class="schema-fields">${renderSchemaFields(property, path)}</div></fieldset>`;
      }
      return renderSchemaInput(name, path, property, required.has(name));
    }).join("");
  }

  function renderSchemaInput(name, path, schema, required) {
    const label = `${labelFor(name)}${required ? " *" : ""}`;
    const common = `data-param-path="${escapeHTML(path)}" data-param-type="${escapeHTML(schema.type || "string")}" data-param-format="${escapeHTML(schema.format || "")}" ${required ? "required" : ""}`;
    const classes = ["description", "source_cidr"].includes(name) ? "field wide" : "field";
    if (schema.enum) {
      return `<label class="${classes}"><span>${escapeHTML(label)}</span><select ${common}>${required ? "" : `<option value="">Не задано</option>`}${schema.enum.map((value) => `<option value="${escapeHTML(value)}">${escapeHTML(value)}</option>`).join("")}</select></label>`;
    }
    if (schema.type === "boolean") {
      return `<label class="${classes}"><span>${escapeHTML(label)}</span><select ${common}>${required ? "" : `<option value="">Не задано</option>`}<option value="true">Да</option><option value="false">Нет</option></select></label>`;
    }
    const options = optionsForParam(name);
    if (options.length) {
      return `<label class="${classes}"><span>${escapeHTML(label)}</span><select ${common}><option value="">Выберите…</option>${options.map((item) => `<option value="${escapeHTML(item.value)}" ${isPreferredParamValue(name, item.value) ? "selected" : ""}>${escapeHTML(item.label)}</option>`).join("")}</select></label>`;
    }
    const type = schema.type === "integer" || schema.type === "number" ? "number" : schema.format === "date-time" ? "datetime-local" : "text";
    const min = schema.minimum != null ? `min="${schema.minimum}"` : "";
    const placeholder = placeholderFor(name);
    return `<label class="${classes}"><span>${escapeHTML(label)}</span><input type="${type}" ${common} ${min} placeholder="${escapeHTML(placeholder)}"></label>`;
  }

  function optionsForParam(name) {
    if (name === "server_id") return runtime.resources.servers.map((server) => ({ value: server.server_id, label: `${server.name} · ${server.role} · ${server.status}` }));
    if (name === "database_id" || name === "expected_current_database_id") return runtime.resources.servers.flatMap((server) => server.database_ids.map((id) => ({ value: id, label: `${id} · ${server.name}` })));
    if (name === "instance_type") return ["backend.standard", "db.small", "db.medium", "db.large"].map((value) => ({ value, label: value }));
    return [];
  }

  function isPreferredParamValue(name, value) {
    if (name === "server_id") return value === runtime.activeTargetServer;
    if (name === "database_id" && runtime.activeTargetServer) {
      const server = runtime.resources.servers.find((item) => item.server_id === runtime.activeTargetServer);
      return server?.database_ids.length === 1 && server.database_ids[0] === value;
    }
    return false;
  }

  function renderTargetAuth() {
    const available = Object.entries(runtime.credentials);
    const credential = runtime.credentials[runtime.activeTargetServer] || { username: "", password: "" };
    const server = runtime.resources.servers.find((item) => item.server_id === runtime.activeTargetServer);
    const hasCurrentCredential = credential.credential_id && credential.credential_id === server?.credential_id;
    const state = !server ? "Сначала выберите сервер или БД в параметрах команды." : hasCurrentCredential ? `Готово: ${server.server_id}, credentials v${credential.version}.` : `Для ${server.server_id} credentials ещё не загружены.`;
    return `<section class="target-auth-box" id="target-auth-box">
      <div class="target-auth-heading"><div><h3>Авторизация команды</h3><p>Для этой команды нужны оба уровня доступа.</p></div><span class="badge ${hasCurrentCredential ? "good" : "warn"}">${hasCurrentCredential ? "готово" : "нужен доступ"}</span></div>
      <div class="auth-flow"><div><b>1</b><span><strong>Basic Auth панели</strong>Автоматически уходит в HTTP-заголовке.</span></div><i>→</i><div><b>2</b><span><strong>Target Auth сервера</strong>Уходит внутри JSON-команды.</span></div></div>
      <div class="target-auth-row"><label class="field"><span>Профиль целевого сервера</span><select id="target-server"><option value="">Выберите credentials…</option>${available.map(([serverId, value]) => `<option value="${escapeHTML(serverId)}" ${runtime.activeTargetServer === serverId ? "selected" : ""}>${escapeHTML(serverId)} · v${value.version}</option>`).join("")}</select></label><button class="button secondary compact" id="fetch-target-credential" type="button" ${server?.credential_id ? "" : "disabled"}>${hasCurrentCredential ? "Подставить credentials" : "Получить credentials"}</button></div>
      <div class="inline-fields"><label class="field"><span>Username</span><input id="target-username" type="text" value="${escapeHTML(credential.username)}"></label><label class="field"><span>Password</span><input id="target-password" type="password" value="${escapeHTML(credential.password)}"></label></div>
      <div class="target-auth-state" id="target-auth-state">${escapeHTML(state)} Пароль будет скрыт в консоли.</div></section>`;
  }

  function commandTargetServerID() {
    const direct = document.querySelector('[data-param-path="server_id"]')?.value;
    if (direct) return direct;
    const databaseID = document.querySelector('[data-param-path="database_id"]')?.value;
    if (databaseID) return runtime.resources.servers.find((server) => server.database_ids.includes(databaseID))?.server_id || "";
    return runtime.activeTargetServer;
  }

  function refreshTargetAuthBox() {
    const box = document.querySelector("#target-auth-box");
    if (!box) return;
    box.replaceWith(htmlElement(renderTargetAuth()));
    bindTargetAuthEvents();
  }

  function bindTargetAuthEvents() {
    document.querySelector("#target-server")?.addEventListener("change", (event) => {
      runtime.activeTargetServer = event.target.value;
      const server = runtime.resources.servers.find((item) => item.server_id === runtime.activeTargetServer);
      const serverInput = document.querySelector('[data-param-path="server_id"]');
      if (serverInput && server) serverInput.value = server.server_id;
      const databaseInput = document.querySelector('[data-param-path="database_id"]');
      if (databaseInput && server?.database_ids.length === 1) databaseInput.value = server.database_ids[0];
      refreshTargetAuthBox();
    });
    document.querySelector("#fetch-target-credential")?.addEventListener("click", () => {
      const serverID = commandTargetServerID();
      const server = runtime.resources.servers.find((item) => item.server_id === serverID);
      if (!server) return toast("Сначала выберите ресурс", "Укажите сервер или базу данных в параметрах команды.", "error");
      fetchCredential(server.server_id, server.credential_id, true);
    });
  }

  function defaultRawEnvelope(definition) {
    const result = { request_id: requestId("cmd"), command: definition.command, params: defaultObject(definition.params_schema) };
    if (definition.target_auth_required) result.target_auth = { username: "", password: "" };
    return result;
  }

  function defaultObject(schema) {
    const result = {};
    const required = new Set(schema?.required || []);
    Object.entries(schema?.properties || {}).forEach(([name, property]) => {
      if (!required.has(name)) return;
      if (property.type === "object") result[name] = defaultObject(property);
      else if (property.enum) result[name] = property.enum[0];
      else if (property.type === "boolean") result[name] = true;
      else if (property.type === "integer" || property.type === "number") result[name] = property.minimum || 0;
      else result[name] = "";
    });
    return result;
  }

  function renderConsole() {
    const entries = runtime.console.slice().reverse().map((entry) => `<article class="console-entry ${entry.kind}"><div class="console-meta"><span class="console-kind">${escapeHTML(entry.label)}</span><time>${formatClock(entry.at)}</time></div><pre>${entry.request ? `<span class="request">${escapeHTML(entry.request)}</span>\n` : ""}${escapeHTML(entry.output)}</pre></article>`).join("");
    return `<section class="console" id="command-console"><header class="console-head"><h3>Command output</h3><button class="button compact" type="button" id="clear-console">Очистить</button></header><div class="console-body" id="console-body">${entries || `<div class="console-empty">Результаты команд появятся здесь</div>`}</div></section>`;
  }

  function bindWorkspaceEvents(readonly) {
    document.querySelector("#toggle-password")?.addEventListener("click", () => {
      const input = document.querySelector("#panel-password");
      input.type = input.type === "password" ? "text" : "password";
    });
    document.querySelector("#save-auth")?.addEventListener("click", savePanelAuth);
    document.querySelector("#stop-on-log-error")?.addEventListener("change", (event) => { runtime.stopOnLogError = event.target.checked; });
    document.querySelectorAll("[data-advance]").forEach((button) => button.addEventListener("click", () => advanceTime(Number(button.dataset.advance))));
    document.querySelector("#advance-custom")?.addEventListener("click", () => {
      const value = Number(document.querySelector("#duration-value").value);
      const unit = Number(document.querySelector("#duration-unit").value);
      if (!Number.isFinite(value) || value <= 0 || value * unit < 300) return toast("Минимум 5 минут", "Увеличьте произвольную длительность.", "error");
      advanceTime(Math.round(value * unit));
    });
    document.querySelector("#finish-run")?.addEventListener("click", finishRun);
    document.querySelector("#refresh-resources")?.addEventListener("click", refreshDashboard);
    document.querySelector("#refresh-inbox")?.addEventListener("click", refreshDashboard);
    document.querySelector("#refresh-operations")?.addEventListener("click", () => pollOperations(true));
    document.querySelectorAll("[data-credential-server]").forEach((button) => button.addEventListener("click", () => fetchCredential(button.dataset.credentialServer, button.dataset.credentialId)));
    document.querySelectorAll("[data-metric-range]").forEach((button) => button.addEventListener("click", () => changeMetricRange(button.dataset.metricRange)));
    document.querySelectorAll("[data-message-id]").forEach((button) => button.addEventListener("click", () => selectMessage(button)));
    document.querySelectorAll("[data-command-group]").forEach((button) => button.addEventListener("click", () => { runtime.commandGroup = button.dataset.commandGroup; renderWorkspace(); }));
    document.querySelectorAll("[data-command]").forEach((button) => button.addEventListener("click", () => { runtime.selectedCommand = button.dataset.command; renderWorkspace(); }));
    document.querySelectorAll("[data-command-mode]").forEach((button) => button.addEventListener("click", () => { runtime.commandMode = button.dataset.commandMode; renderWorkspace(); }));
    document.querySelectorAll('[data-param-path="server_id"], [data-param-path="database_id"]').forEach((input) => input.addEventListener("change", () => {
      runtime.activeTargetServer = commandTargetServerID();
      refreshTargetAuthBox();
    }));
    bindTargetAuthEvents();
    document.querySelector("#command-form")?.addEventListener("submit", executeCommand);
    document.querySelector("#clear-console")?.addEventListener("click", () => { runtime.console = []; renderWorkspace(); });
  }

  async function savePanelAuth() {
    runtime.auth = { username: document.querySelector("#panel-username").value.trim(), password: document.querySelector("#panel-password").value };
    sessionStorage.setItem(authKey(runtime.runId), JSON.stringify(runtime.auth));
    await loadCatalog();
    renderWorkspace();
    if (runtime.catalog) toast("Панель подключена", "Каталог команд загружен.");
    else toast("Авторизация не прошла", runtime.catalogError, "error");
  }

  async function advanceTime(seconds) {
    const runId = runtime.runId;
    const controls = document.querySelectorAll(".time-controls button, .time-controls input, .time-controls select");
    controls.forEach((control) => control.disabled = true);
    try {
      const payload = { request_id: requestId("advance"), duration_seconds: seconds };
      if (runtime.stopOnLogError) payload.stop_when = { new_log_errors: 1 };
      const result = await runApi(`/v2/runs/${runId}/time/advance`, { method: "POST", body: JSON.stringify(payload) });
      const title = result.stop_reason === "log_error" ? "Остановлено на ошибке" : result.stop_reason === "run_completed" ? "Run завершён" : "Время промотано";
      const detail = `${formatDuration(result.clock.applied_advance_seconds)} · ${formatCompact(result.processed_events)} событий${result.stop_reason === "log_error" ? " · откройте логи" : ""}`;
      toast(title, detail, result.stop_reason === "log_error" ? "error" : "");
      if (isRunDashboard(runId)) await refreshDashboard();
    } catch (error) {
      toast(error.code, error.message, "error");
      if (isRunDashboard(runId)) renderWorkspace();
    }
  }

  async function finishRun() {
    const confirmed = await confirmAction("Завершить недельный run?", "Симуляция будет промотана до конца. Отменить это действие после выполнения нельзя.");
    if (!confirmed) return;
    clearTimeout(operationTimer);
    const runId = runtime.runId;
    const buttons = document.querySelectorAll(".time-controls button");
    const finishButton = document.querySelector("#finish-run");
    const total = Math.max(0, Math.ceil(runtime.overview.clock.remaining_seconds));
    let remaining = total;
    let processedEvents = 0;
    buttons.forEach((button) => button.disabled = true);
    if (finishButton) finishButton.setAttribute("aria-busy", "true");
    toast("Завершаем run", "Сервер обрабатывает все события до конца недели.");
    try {
      while (remaining > 0) {
        const duration = Math.max(300, Math.min(6 * 3600, remaining));
        const result = await runApi(`/v2/runs/${runId}/time/advance`, { method: "POST", body: JSON.stringify({ request_id: requestId("finish"), duration_seconds: duration }) });
        processedEvents += result.processed_events;
        remaining = Math.max(0, Math.ceil(result.clock.remaining_seconds));
        const progress = total > 0 ? Math.min(100, Math.round((1 - remaining / total) * 100)) : 100;
        if (finishButton) finishButton.textContent = `Завершаем · ${progress}%`;
        const clock = document.querySelector(".workspace-actions .sim-clock");
        if (clock) clock.innerHTML = `<time>${formatDateTime(result.clock.simulation_time)}</time><span>${formatDuration(remaining)} до конца недели</span>`;
      }
      toast("Run завершён", `${formatCompact(processedEvents)} событий обработано`);
      if (isRunDashboard(runId)) await refreshDashboard();
      else if (location.pathname.replace(/\/+$/, "") === "/ui/history") await renderHistoryPage();
    } catch (error) {
      toast(error.code, error.message, "error");
      if (isRunDashboard(runId)) await refreshDashboard();
    }
  }

  function isRunDashboard(runId) {
    return runtime.runId === runId && location.pathname.replace(/\/+$/, "") === `/ui/runs/${runId}`;
  }

  async function refreshDashboard() {
    try {
      runtime.overview = await runApi(`/v2/runs/${runtime.runId}/overview`);
      runtime.resources = await runApi(`/v2/runs/${runtime.runId}/resources`);
      runtime.inbox = await runApi(`/v2/runs/${runtime.runId}/inbox?limit=200`);
      runtime.metrics = await fetchMetrics();
      renderWorkspace();
      pollOperations();
    } catch (error) { toast(error.code, error.message, "error"); }
  }

  async function changeMetricRange(range) {
    runtime.metricRange = range;
    document.querySelectorAll("[data-metric-range]").forEach((button) => button.classList.toggle("active", button.dataset.metricRange === range));
    try {
      runtime.metrics = await fetchMetrics();
      renderWorkspace();
    } catch (error) { toast("Графики не обновлены", error.message, "error"); }
  }

  function selectMessage(button) {
    document.querySelectorAll(".inbox-item").forEach((item) => item.classList.toggle("active", item === button));
    const message = runtime.inbox.messages.find((item) => item.message_id === button.dataset.messageId);
    document.querySelector(".message-view")?.replaceWith(htmlElement(messageView(message)));
  }

  async function fetchCredential(serverId, credentialId, preserveCommand = false) {
    if (!basicHeader()) return toast("Нужен Basic Auth", "Введите доступ панели сверху.", "error");
    if (runtime.credentials[serverId]?.credential_id === credentialId) {
      runtime.activeTargetServer = serverId;
      if (preserveCommand) refreshTargetAuthBox();
      else renderWorkspace();
      toast("Credentials подставлены", serverId);
      return;
    }
    try {
      const result = await runApi(`/v2/runs/${runtime.runId}/credentials/${encodeURIComponent(credentialId)}`, { basic: true });
      runtime.credentials[serverId] = result.credential;
      runtime.activeTargetServer = serverId;
      sessionStorage.setItem(credentialsKey(runtime.runId), JSON.stringify(runtime.credentials));
      addConsole("ok", "CREDENTIALS", `GET /credentials/${credentialId}`, JSON.stringify({ ...result.credential, password: "[REDACTED]" }, null, 2));
      if (preserveCommand) refreshTargetAuthBox();
      else renderWorkspace();
      toast("Credentials получены", `${serverId} · версия ${result.credential.version}`);
    } catch (error) { toast(error.code, error.message, "error"); }
  }

  async function executeCommand(event) {
    event.preventDefault();
    const definition = runtime.catalog.find((item) => item.command === runtime.selectedCommand);
    let envelope;
    try {
      if (runtime.commandMode === "raw") {
        envelope = JSON.parse(document.querySelector("#raw-command").value);
      } else {
        if (!event.currentTarget.reportValidity()) return;
        envelope = { request_id: requestId("cmd"), command: definition.command, params: collectParams(event.currentTarget) };
        if (definition.target_auth_required) {
          const username = document.querySelector("#target-username").value.trim();
          const password = document.querySelector("#target-password").value;
          if (!username || !password) {
            toast("Нужен Target Auth", "Нажмите «Получить credentials» для выбранного ресурса.", "error");
            return;
          }
          envelope.target_auth = { username, password };
        }
      }
    } catch (error) {
      toast("Некорректный JSON", error.message, "error");
      return;
    }
    if (!envelope.request_id) envelope.request_id = requestId("cmd");
    const redacted = JSON.parse(JSON.stringify(envelope));
    if (redacted.target_auth) redacted.target_auth.password = "[REDACTED]";
    const submit = event.currentTarget.querySelector("button[type=submit]");
    submit.disabled = true;
    try {
      const result = await runApi(`/v2/runs/${runtime.runId}/control/commands`, { method: "POST", basic: true, body: JSON.stringify(envelope) });
      addConsole(result.operation_id ? "pending" : "ok", result.operation_id ? "ACCEPTED" : "OK", `$ ${envelope.command}\n${JSON.stringify(redacted, null, 2)}`, JSON.stringify(result, null, 2));
      if (result.operation_id) addOperation(result);
      await refreshDashboard();
    } catch (error) {
      addConsole("error", error.code || "ERROR", `$ ${envelope.command}\n${JSON.stringify(redacted, null, 2)}`, JSON.stringify(error.payload || { error: error.code, message: error.message }, null, 2));
      renderWorkspace();
    }
  }

  function collectParams(form) {
    const result = {};
    form.querySelectorAll("[data-param-path]").forEach((input) => {
      if (input.value === "") return;
      let value = input.value;
      if (input.dataset.paramType === "integer" || input.dataset.paramType === "number") value = Number(value);
      if (input.dataset.paramType === "boolean") value = value === "true";
      if (input.dataset.paramFormat === "date-time") value = new Date(value).toISOString();
      setPath(result, input.dataset.paramPath.split("."), value);
    });
    return result;
  }

  function setPath(target, path, value) {
    const last = path.pop();
    const parent = path.reduce((object, key) => object[key] ||= {}, target);
    parent[last] = value;
  }

  function addConsole(kind, label, request, output) {
    runtime.console.push({ kind, label, request, output, at: new Date() });
    if (runtime.console.length > 100) runtime.console.shift();
  }

  function addOperation(result) {
    if (!runtime.operations.some((item) => item.operation_id === result.operation_id)) runtime.operations.push(result);
    persistOperations();
  }

  function persistOperations() {
    sessionStorage.setItem(operationsKey(runtime.runId), JSON.stringify(runtime.operations.map((item) => item.operation_id)));
  }

  async function pollOperations(force = false) {
    clearTimeout(operationTimer);
    const pending = runtime.operations.filter((item) => ["queued", "running"].includes(item.status));
    if (!pending.length && !force) return;
    for (const operation of (force ? runtime.operations : pending)) {
      try {
        const result = await runApi(`/v2/runs/${runtime.runId}/operations/${operation.operation_id}`);
        const index = runtime.operations.findIndex((item) => item.operation_id === result.operation_id);
        if (index >= 0) runtime.operations[index] = result;
        if (["succeeded", "failed"].includes(result.status) && operation.status !== result.status) {
          addConsole(result.status === "succeeded" ? "ok" : "error", `OPERATION ${result.status.toUpperCase()}`, `$ operation ${result.operation_id}`, JSON.stringify(result, null, 2));
          toast(`Операция ${result.status}`, result.command, result.status === "failed" ? "error" : "");
        }
      } catch (error) {
        if (error.code !== "CONCURRENT_RUN_REQUEST") toast("Операция недоступна", error.message, "error");
      }
    }
    persistOperations();
    refreshOperationSurfaces();
    if (runtime.operations.some((item) => ["queued", "running"].includes(item.status)) && runtime.overview.status === "running") {
      operationTimer = setTimeout(() => pollOperations(), 2500);
    }
  }

  function refreshOperationSurfaces() {
    const operations = document.querySelector("#operations-panel");
    if (operations) operations.replaceWith(htmlElement(renderOperations()));
    const consolePanel = document.querySelector("#command-console");
    if (consolePanel) consolePanel.replaceWith(htmlElement(renderConsole()));
    document.querySelector("#refresh-operations")?.addEventListener("click", () => pollOperations(true));
    document.querySelector("#clear-console")?.addEventListener("click", () => { runtime.console = []; refreshOperationSurfaces(); });
  }

  async function loadLogsPage(runId) {
    loading("Загружаем журнал запросов…");
    runtime = freshRuntime();
    runtime.runId = runId;
    try {
      runtime.overview = await runApi(`/v2/runs/${runId}/overview`);
      const now = new Date(runtime.overview.clock.simulation_time);
      runtime.logFilters.from = toLocalInput(new Date(now.getTime() - 6 * 3600e3));
      runtime.logFilters.to = toLocalInput(now);
      await fetchLogs("");
      renderLogsPage();
    } catch (error) { renderError(error); }
  }

  async function fetchLogs(cursor) {
    const filters = runtime.logFilters;
    const query = new URLSearchParams({ limit: filters.limit || "200" });
    if (filters.from) query.set("from", new Date(filters.from).toISOString());
    if (filters.to) query.set("to", new Date(filters.to).toISOString());
    if (filters.page) query.set("page", filters.page);
    if (filters.presence !== "all") query.set("has_error", filters.presence === "error" ? "true" : "false");
    if (filters.error) { query.set("error", filters.error); query.set("has_error", "true"); }
    if (cursor) query.set("cursor", cursor);
    runtime.logs = await runApi(`/v2/runs/${runtime.runId}/logs?${query}`);
  }

  function renderLogsPage() {
    const filters = runtime.logFilters;
    const logs = runtime.logs.logs || [];
    app.innerHTML = `<section class="logs-head"><div><div class="breadcrumb"><a href="/ui/runs/${runtime.runId}" data-link>Run dashboard</a><span>/</span><span>logs</span></div><h1>Site logs</h1><p class="muted mono">${escapeHTML(runtime.runId)}</p></div><div class="sim-clock"><time>${formatDateTime(runtime.logs.clock.simulation_time)}</time><span>снимок журнала</span></div></section>
      <form class="logs-filter" id="logs-filter">
        <label class="field"><span>Ошибка</span><select id="filter-presence"><option value="all" ${filters.presence === "all" ? "selected" : ""}>Все запросы</option><option value="error" ${filters.presence === "error" ? "selected" : ""}>Есть ошибка</option><option value="success" ${filters.presence === "success" ? "selected" : ""}>Без ошибки</option></select></label>
        <label class="field"><span>Тип ошибки</span><select id="filter-error"><option value="">Любой тип</option>${errorCodes.map((code) => `<option value="${code}" ${filters.error === code ? "selected" : ""}>${code}</option>`).join("")}</select></label>
        <label class="field"><span>От</span><input id="filter-from" type="datetime-local" value="${escapeHTML(filters.from)}"></label>
        <label class="field"><span>До</span><input id="filter-to" type="datetime-local" value="${escapeHTML(filters.to)}"></label>
        <label class="field"><span>Страница</span><select id="filter-page"><option value="">Все</option><option value="product_list" ${filters.page === "product_list" ? "selected" : ""}>product_list</option><option value="product_page" ${filters.page === "product_page" ? "selected" : ""}>product_page</option></select></label>
        <label class="field"><span>Лимит</span><select id="filter-limit"><option>100</option><option ${filters.limit === "200" ? "selected" : ""}>200</option><option ${filters.limit === "500" ? "selected" : ""}>500</option><option ${filters.limit === "1000" ? "selected" : ""}>1000</option></select></label>
        <button class="button" type="submit">Обновить</button>
      </form>
      <section class="panel history-section"><div class="table-scroll"><table class="data-table"><thead><tr><th>Время</th><th>Запрос</th><th>Источник</th><th>Страница</th><th>Статус</th><th>Latency / load</th><th>Backend</th><th>Ошибка</th></tr></thead><tbody>${logs.map(logRow).join("") || `<tr><td colspan="8" class="table-empty">По выбранным фильтрам записей нет</td></tr>`}</tbody></table></div><div class="logs-pager"><span>Показано ${logs.length} записей</span><div class="panel-tools"><button class="button secondary compact" id="logs-prev" ${runtime.logCursorIndex === 0 ? "disabled" : ""}>← Назад</button><button class="button secondary compact" id="logs-next" ${runtime.logs.next_cursor ? "" : "disabled"}>Дальше →</button></div></div></section>`;
    document.querySelector("#logs-filter").addEventListener("submit", applyLogFilters);
    document.querySelector("#filter-error").addEventListener("change", (event) => { if (event.target.value) document.querySelector("#filter-presence").value = "error"; });
    document.querySelector("#logs-next").addEventListener("click", () => changeLogPage(1));
    document.querySelector("#logs-prev").addEventListener("click", () => changeLogPage(-1));
  }

  function logRow(log) {
    return `<tr><td class="mono nowrap">${formatDateTime(log.timestamp)}</td><td><strong class="mono">${escapeHTML(log.request_id)}</strong><br><span class="muted">${escapeHTML(log.visitor_id || "")}</span></td><td><span class="role">${escapeHTML(log.source)}</span><br><span class="muted mono">${escapeHTML(log.source_ip)} · ${escapeHTML(log.region_code)}</span></td><td class="mono">${escapeHTML(log.page)}${log.product_id ? `<br><span class="muted">${escapeHTML(log.product_id)}</span>` : ""}</td><td><span class="log-status s${log.status}">${log.status}</span></td><td class="mono">${formatNumber(log.latency_ms)} ms<br><span class="muted">${formatCompact(log.load_units)} units</span></td><td class="mono">${escapeHTML(log.server_id || "—")}</td><td class="log-message">${log.error ? `<span class="log-code">${escapeHTML(log.error)}</span>` : ""}${escapeHTML(log.message || "—")}</td></tr>`;
  }

  async function applyLogFilters(event) {
    event.preventDefault();
    runtime.logFilters = {
      presence: document.querySelector("#filter-presence").value,
      error: document.querySelector("#filter-error").value,
      from: document.querySelector("#filter-from").value,
      to: document.querySelector("#filter-to").value,
      page: document.querySelector("#filter-page").value,
      limit: document.querySelector("#filter-limit").value,
    };
    if (runtime.logFilters.from && runtime.logFilters.to && new Date(runtime.logFilters.to) < new Date(runtime.logFilters.from)) return toast("Неверный диапазон", "Дата «до» раньше даты «от».", "error");
    runtime.logCursors = [""];
    runtime.logCursorIndex = 0;
    try { await fetchLogs(""); renderLogsPage(); } catch (error) { toast(error.code, error.message, "error"); }
  }

  async function changeLogPage(direction) {
    if (direction > 0) {
      if (!runtime.logs.next_cursor) return;
      runtime.logCursors = runtime.logCursors.slice(0, runtime.logCursorIndex + 1);
      runtime.logCursors.push(runtime.logs.next_cursor);
      runtime.logCursorIndex++;
    } else if (runtime.logCursorIndex > 0) runtime.logCursorIndex--;
    try { await fetchLogs(runtime.logCursors[runtime.logCursorIndex]); renderLogsPage(); } catch (error) { toast(error.code, error.message, "error"); }
  }

  function labelFor(name) {
    return ({ server_id: "Сервер", database_id: "База данных", expected_current_database_id: "Текущая БД", instance_type: "Тип инстанса", rule_id: "ID правила", backup_id: "Backup ID", source_cidr: "Подсеть CIDR", region_code: "Регион", user_agent: "User-Agent", expires_at: "Истекает", enabled: "Включено", priority: "Приоритет", action: "Действие", role: "Роль", name: "Имя", operator: "Сравнение", value: "Значение", match: "Условия" })[name] || name.replaceAll("_", " ");
  }
  function placeholderFor(name) {
    return ({ rule_id: "deny-suspicious", backup_id: "backup-…", source_cidr: "203.0.113.0/24", region_code: "US", name: "backend-peak", value: "WebCamera" })[name] || "";
  }
  function statusClass(status) {
    if (["healthy", "active", "succeeded", "running"].includes(status)) return status;
    if (["degraded", "provisioning", "queued"].includes(status)) return status;
    if (["unavailable", "failed", "stopped"].includes(status)) return status;
    return "neutral";
  }
  function safeRatio(value, total) { return total > 0 ? value / total : 0; }
  function percent(value, digits = 1) { return `${(Number(value || 0) * 100).toFixed(digits)}%`; }
  function formatNumber(value) { return new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 1 }).format(Number(value || 0)); }
  function formatCompact(value) { return new Intl.NumberFormat("ru-RU", { notation: "compact", maximumFractionDigits: 1 }).format(Number(value || 0)); }
  function formatMoney(minor, currency = "USD") { return new Intl.NumberFormat("ru-RU", { style: "currency", currency: currency || "USD", minimumFractionDigits: 0, maximumFractionDigits: 2 }).format(Number(minor || 0) / 100); }
  function formatBytes(bytes) {
    const value = Number(bytes || 0);
    if (value >= 1024 ** 3) return `${formatNumber(value / 1024 ** 3)} GiB`;
    if (value >= 1024 ** 2) return `${formatNumber(value / 1024 ** 2)} MiB`;
    return `${formatCompact(value)} B`;
  }
  function toGiB(value) { return Number(value || 0) / 1024 ** 3; }
  function formatDuration(seconds) {
    const value = Math.max(0, Math.round(Number(seconds || 0)));
    if (value >= 86400) return `${Math.floor(value / 86400)}д ${Math.floor(value % 86400 / 3600)}ч`;
    if (value >= 3600) return `${Math.floor(value / 3600)}ч ${Math.floor(value % 3600 / 60)}м`;
    if (value >= 60) return `${Math.floor(value / 60)}м`;
    return `${value}с`;
  }
  function formatDateTime(value) { return new Intl.DateTimeFormat("ru-RU", { day: "2-digit", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", timeZoneName: "short" }).format(new Date(value)); }
  function formatShortDate(value) { return new Intl.DateTimeFormat("ru-RU", { day: "2-digit", month: "short", hour: "2-digit", minute: "2-digit" }).format(new Date(value)); }
  function formatShortTime(value) { return new Intl.DateTimeFormat("ru-RU", { day: "2-digit", month: "short", hour: "2-digit", minute: "2-digit" }).format(new Date(value)); }
  function formatClock(value) { return new Intl.DateTimeFormat("ru-RU", { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(new Date(value)); }
  function toLocalInput(date) {
    const shifted = new Date(date.getTime() - date.getTimezoneOffset() * 60000);
    return shifted.toISOString().slice(0, 16);
  }
  function htmlElement(markup) {
    const template = document.createElement("template");
    template.innerHTML = markup.trim();
    return template.content.firstElementChild;
  }

  route();
})();
