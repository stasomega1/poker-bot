const tg = window.Telegram?.WebApp;
if (tg) {
  tg.ready();
  tg.expand();
}

const app = document.getElementById("app");
const initData = tg?.initData || "";
const params = new URLSearchParams(window.location.search);
const sessionId =
  params.get("session_id") ||
  params.get("session") ||
  params.get("startapp") ||
  params.get("tgWebAppStartParam") ||
  "";
const isLocalHost = ["127.0.0.1", "localhost"].includes(window.location.hostname);

let bill = null;
let pollTimer = null;
let busy = false;
let itemOrderBySession = new Map();

function money(value) {
  const number = Number(value || 0);
  return new Intl.NumberFormat("ru-KZ", {
    maximumFractionDigits: 0,
  }).format(number) + " тг";
}

function dateTime(value) {
  if (!value) return "";
  return new Intl.DateTimeFormat("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function statusText(status) {
  if (status === "active") return "Активен";
  if (status === "finished") return "Закрыт";
  if (status === "cancelled") return "Отменен";
  return status || "Неизвестно";
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      "X-Telegram-Init-Data": initData,
      ...(options.headers || {}),
    },
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error || "Ошибка запроса");
  }
  return payload;
}

async function load({ silent = false } = {}) {
  if (!sessionId) {
    renderState("Счет не указан", "Откройте Mini App из сообщения активного счета в группе.");
    return;
  }
  if (!initData && !isLocalHost) {
    renderState("Откройте внутри Telegram", "Mini App требует авторизацию Telegram.");
    return;
  }
  if (!silent) renderLoading();
  try {
    bill = await api(`/api/webapp/bills/${encodeURIComponent(sessionId)}`);
    renderBill();
  } catch (error) {
    if (!silent) renderState("Не удалось открыть счет", error.message);
  }
}

async function mutate(path, body) {
  if (busy) return;
  busy = true;
  try {
    bill = await api(path, {
      method: "POST",
      body: JSON.stringify(body || {}),
    });
    renderBill();
  } catch (error) {
    tg?.showAlert ? tg.showAlert(error.message) : alert(error.message);
  } finally {
    busy = false;
  }
}

function renderLoading() {
  app.innerHTML = `
    <section class="state">
      <div class="loader"></div>
      <p>Загружаю счет...</p>
    </section>
  `;
}

function renderState(title, message) {
  app.innerHTML = `
    <section class="state">
      <h2>${escapeHTML(title)}</h2>
      <p>${escapeHTML(message)}</p>
    </section>
  `;
}

function renderBill() {
  const session = bill.session;
  const progress = bill.progress;
  const percent = progress.totalUnits > 0
    ? Math.round((progress.assignedUnits / progress.totalUnits) * 100)
    : 0;

  const assignmentsByItem = new Map();
  for (const assignment of bill.assignments) {
    const rows = assignmentsByItem.get(assignment.itemIndex) || [];
    rows.push(assignment);
    assignmentsByItem.set(assignment.itemIndex, rows);
  }

  const sortedItems = getStableSortedItems(session.id, bill.items);

  app.innerHTML = `
    <header class="topbar">
      <div class="title-row">
        <div>
          <h1>${escapeHTML(session.merchantName || "Счет")}</h1>
          <p class="subtitle">${escapeHTML(session.chatTitle || "")} · ${dateTime(session.createdAt)}</p>
        </div>
        <span class="status ${session.status === "active" ? "active" : ""}">${statusText(session.status)}</span>
      </div>
      <div class="progress">
        <div class="progress-label">
          <span>Распределено ${progress.assignedUnits} из ${progress.totalUnits}</span>
          <span>Осталось ${progress.remainingUnits}</span>
        </div>
        <div class="progress-track"><div class="progress-fill" style="width:${percent}%"></div></div>
      </div>
    </header>

    <section class="summary-grid">
      <div class="metric"><span>Итого</span><strong>${money(session.totalAmount)}</strong></div>
      <div class="metric"><span>Сервис</span><strong>${money(session.serviceAmount)}</strong></div>
      <div class="metric"><span>Плательщик</span><strong>${escapeHTML(session.payerName || "-")}</strong></div>
    </section>

    <section class="items">
      ${sortedItems.map((item) => renderItem(item, assignmentsByItem.get(item.index) || [])).join("")}
    </section>

    <footer class="bottom-sheet">
      <div class="my-total">
        <div>
          <span>Мой счет</span>
          <strong>${money(bill.me.grandTotal)}</strong>
        </div>
        <span>${money(bill.me.itemsTotal)} + сервис ${money(bill.me.serviceShare)}</span>
      </div>
      ${renderActions()}
    </footer>
  `;

  bindButtons();
}

function getStableSortedItems(sessionId, items) {
  let order = itemOrderBySession.get(sessionId);
  if (!order) {
    order = buildInitialItemOrder(items);
    itemOrderBySession.set(sessionId, order);
  }

  const knownIndexes = new Set(order);
  for (const item of items) {
    if (!knownIndexes.has(item.index)) {
      order.push(item.index);
      knownIndexes.add(item.index);
    }
  }

  const orderPosition = new Map(order.map((itemIndex, position) => [itemIndex, position]));
  return [...items].sort((a, b) => {
    const aPosition = orderPosition.get(a.index);
    const bPosition = orderPosition.get(b.index);
    if (aPosition !== undefined && bPosition !== undefined) {
      return aPosition - bPosition;
    }
    return a.index - b.index;
  });
}

function buildInitialItemOrder(items) {
  return [...items]
    .sort((a, b) => {
      const aGroup = initialItemSortGroup(a);
      const bGroup = initialItemSortGroup(b);
      if (aGroup !== bGroup) return aGroup - bGroup;
      return a.index - b.index;
    })
    .map((item) => item.index);
}

function initialItemSortGroup(item) {
  if (item.remaining > 0) return 0;
  return 1;
}

function renderItem(item, assignments) {
  const canAdjust = bill.permissions.canAdjust && bill.session.status === "active";
  const canSplit = bill.permissions.canSplit && item.quantity > 1;
  const canMinus = canAdjust && item.myQuantity > 0;
  const canPlus = canAdjust;
  const mineClass = item.myQuantity > 0 ? " item-mine" : "";

  return `
    <article class="item${mineClass}">
      <div class="item-head">
        <div>
          <h2 class="item-name">#${item.index} ${escapeHTML(item.name)}</h2>
          <p class="item-meta">${item.quantity} шт · ${money(item.unitPrice)} · строка ${money(item.lineTotal)}</p>
        </div>
        <div class="item-state">
          <strong>${item.assigned}/${item.effectiveQuantity}</strong>
          <span>${item.remaining > 0 ? `ост. ${item.remaining}` : "готово"}</span>
        </div>
      </div>
      ${renderExpectedParticipants(item, canAdjust)}
      <div class="controls">
        <button class="icon-button" data-adjust="${item.index}" data-delta="-1" ${canMinus ? "" : "disabled"}>−</button>
        <div class="my-count">мое: ${item.myQuantity}</div>
        <button class="icon-button primary" data-adjust="${item.index}" data-delta="1" ${canPlus ? "" : "disabled"}>+</button>
      </div>
      ${assignments.length ? `
        <div class="assignments">
          ${assignments.map((assignment) => `
            <div class="assignment">
              <span>${escapeHTML(assignment.userName)}</span>
              <strong>${assignment.quantity}</strong>
            </div>
          `).join("")}
        </div>
      ` : ""}
      ${canSplit ? `<button class="split-button" data-split="${item.index}">Разбить по одной</button>` : ""}
    </article>
  `;
}

function renderExpectedParticipants(item, canAdjust) {
  if (item.quantity !== 1) {
    return "";
  }

  const optionLimit = Math.max(8, item.expectedParticipants, item.assigned);
  const options = [];
  for (let value = 1; value <= optionLimit; value += 1) {
    options.push(`<option value="${value}" ${value === item.expectedParticipants ? "selected" : ""}>${value}</option>`);
  }

  return `
    <div class="participants-row">
      <label for="participants-${item.index}">Делят на</label>
      <select
        id="participants-${item.index}"
        class="participants-select"
        data-expected-participants="${item.index}"
        ${canAdjust ? "" : "disabled"}
      >
        ${options.join("")}
      </select>
      <span>чел.</span>
    </div>
  `;
}

function renderActions() {
  const canFinish = bill.permissions.canFinish;
  return `
    <div class="actions${canFinish ? "" : " actions-single"}">
      ${canFinish ? `
        <button class="action-button primary" data-finish="${bill.progress.remainingUnits > 0 ? "force" : "normal"}">
          Закрыть счет
        </button>
      ` : ""}
      <button class="action-button ${canFinish ? "danger" : "secondary"}" data-close-app>Закрыть бота</button>
    </div>
  `;
}

function bindButtons() {
  document.querySelectorAll("[data-adjust]").forEach((button) => {
    button.addEventListener("click", () => {
      const itemIndex = button.dataset.adjust;
      const delta = Number(button.dataset.delta);
      mutate(`/api/webapp/bills/${encodeURIComponent(sessionId)}/items/${itemIndex}/adjust`, { delta });
    });
  });
  document.querySelectorAll("[data-split]").forEach((button) => {
    button.addEventListener("click", () => {
      const itemIndex = button.dataset.split;
      confirmAction("Разбить эту позицию по одной?", () => {
        mutate(`/api/webapp/bills/${encodeURIComponent(sessionId)}/items/${itemIndex}/split`);
      });
    });
  });
  document.querySelectorAll("[data-expected-participants]").forEach((select) => {
    select.addEventListener("change", () => {
      const itemIndex = select.dataset.expectedParticipants;
      const expectedParticipants = Number(select.value);
      mutate(`/api/webapp/bills/${encodeURIComponent(sessionId)}/items/${itemIndex}/expected-participants`, {
        expectedParticipants,
      });
    });
  });
  document.querySelectorAll("[data-finish]").forEach((button) => {
    button.addEventListener("click", () => {
      const force = button.dataset.finish === "force";
      const text = force
        ? "Остались нераспределенные позиции. Закрыть счет принудительно?"
        : "Закрыть счет?";
      confirmAction(text, () => {
        mutate(`/api/webapp/bills/${encodeURIComponent(sessionId)}/finish`, { force });
      });
    });
  });
  document.querySelectorAll("[data-close-app]").forEach((button) => {
    button.addEventListener("click", () => {
      closeMiniApp();
    });
  });
}

function closeMiniApp() {
  if (tg?.close) {
    tg.close();
    return;
  }
  if (window.history.length > 1) {
    window.history.back();
    return;
  }
  window.close();
}

function confirmAction(message, onConfirm) {
  if (tg?.showConfirm) {
    tg.showConfirm(message, (confirmed) => {
      if (confirmed) onConfirm();
    });
    return;
  }
  if (confirm(message)) onConfirm();
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

load();
pollTimer = setInterval(() => {
  if (document.visibilityState === "visible" && !busy) {
    load({ silent: true });
  }
}, 2000);

window.addEventListener("beforeunload", () => {
  clearInterval(pollTimer);
});
