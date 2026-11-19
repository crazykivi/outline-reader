"use strict";

const $ = (id) => document.getElementById(id);
const go = () => window.go.main.App;

const ICON_SUN = '<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>';
const ICON_MOON = '<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/></svg>';
const ICON_STAR = '<svg width="11" height="11" viewBox="0 0 24 24" fill="currentColor" stroke="none"><path d="m12 2 3.1 6.3 6.9 1-5 4.9 1.2 6.8-6.2-3.2-6.2 3.2L7 14.2 2 9.3l6.9-1z"/></svg>';

let collections = [];
let index = {};
let expanded = new Set();
let history = [];
let historyIndex = -1;
let skipHistoryPush = false;
let currentOID = null;
let currentDocUpdated = "";
let updateTimer = null;
let searchTimer = null;
let toastTimer = null;
let fullWidth = localStorage.getItem("or.fullWidth") === "1";
let tocOpen = localStorage.getItem("or.tocOpen") === "1";
let fontScale = parseFloat(localStorage.getItem("or.fontScale") || "1");
let lastTreeClick = { oid: null, time: 0 };
let currentDocUrl = "";
let lockHasDB = false, lockPortable = false, lockLegacy = false, idleTimer = null;
let isPortable = false, captureProtect = false;
let favorites = [];
let activeSource = "tree";
let clipTimer = null;
let guardTimer = null, guardLeft = 0;

const IDLE_LOCK_MS = 5 * 60 * 1000;
const palette = { open: false, items: [], sel: 0, timer: null };

const find = {
    open: false, matches: [], current: -1,
    caseSensitive: false, regex: false, wrap: true,
    timer: null, lastRun: "",
};
const FIND_LIMIT = 10000;

try { expanded = new Set(JSON.parse(localStorage.getItem("or.expanded") || "[]")); } catch (e) {}
if (!(fontScale >= 0.7 && fontScale <= 2.0)) fontScale = 1;

document.addEventListener("DOMContentLoaded", async () => {
    applyTheme(localStorage.getItem("or.theme") || "dark");
    applyFullWidth(fullWidth);
    applyFontScale(fontScale);
    bindUI();
    initResizer();
    try {
        await waitBindings();
        await bootstrap(true);
    } catch (err) {
        console.error("[UI] bootstrap:", err);
        $("setupError").textContent = "Не удалось прочитать настройки: " + err;
    }
});

function waitBindings(timeoutMs = 5000) {
    return new Promise((resolve, reject) => {
        const start = Date.now();
        (function check() {
            if (window.go && window.go.main && window.go.main.App) return resolve();
            if (Date.now() - start > timeoutMs) return reject(new Error("Wails bindings не готовы"));
            setTimeout(check, 50);
        })();
    });
}

function bindUI() {
    $("connectBtn").addEventListener("click", connect);
    $("syncBtn").addEventListener("click", syncNow);
    $("themeBtn").addEventListener("click", () => {
        applyTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark");
    });
    $("collapseAllBtn").addEventListener("click", () => {
        expanded.clear();
        saveExpanded();
        renderTree();
    });
    $("closeNotification").addEventListener("click", () => { $("updateNotification").style.display = "none"; });
    $("lockBtn").addEventListener("click", unlockDB);
    $("lockReset").addEventListener("click", resetFromLock);
    [$("lockPass"), $("lockPass2")].forEach((el) => {
        el.addEventListener("keydown", (e) => { if (e.key === "Enter") unlockDB(); });
    });

    // меню "три точки"
    $("menuBtn").addEventListener("click", () => {
        $("docMenu").style.display === "none" ? openMenu() : closeMenu();
    });
    document.addEventListener("mousedown", (e) => {
        if ($("docMenu").style.display === "none") return;
        if ($("docMenu").contains(e.target) || $("menuBtn").contains(e.target)) return;
        closeMenu();
    });
    $("menuFind").addEventListener("click", () => {
        if ($("menuFind").classList.contains("disabled")) return;
        closeMenu();
        openFindBar();
    });
    $("menuFullWidth").addEventListener("click", () => { applyFullWidth(!fullWidth); });
    $("menuCapture").addEventListener("click", toggleCapture);
    $("menuLock").addEventListener("click", () => { closeMenu(); lockNow(); });
    $("menuConnection").addEventListener("click", () => { closeMenu(); openConnModal(); });
    $("menuReset").addEventListener("click", () => { closeMenu(); openResetModal(); });

    // модалка подключения
    $("connClose").addEventListener("click", closeConnModal);
    $("connCancel").addEventListener("click", closeConnModal);
    $("connSave").addEventListener("click", saveConnection);
    $("connModal").addEventListener("mousedown", (e) => { if (e.target === $("connModal")) closeConnModal(); });
    [$("connUrl"), $("connKey")].forEach((el) => {
        el.addEventListener("keydown", (e) => { if (e.key === "Enter") saveConnection(); });
    });

    // модалка очистки
    $("resetClose").addEventListener("click", closeResetModal);
    $("resetCancel").addEventListener("click", closeResetModal);
    $("resetConfirm").addEventListener("click", doReset);
    $("resetModal").addEventListener("mousedown", (e) => { if (e.target === $("resetModal")) closeResetModal(); });

    // модалка "О программе"
    $("aboutClose").addEventListener("click", closeAboutModal);
    $("aboutOk").addEventListener("click", closeAboutModal);
    $("aboutRepo").addEventListener("click", (e) => {
        e.preventDefault();
        window.runtime.BrowserOpenURL("https://github.com/crazykivi/outline-reader");
    });
    $("aboutModal").addEventListener("mousedown", (e) => { if (e.target === $("aboutModal")) closeAboutModal(); });

    // глобальный поиск
    $("searchInput").addEventListener("input", () => {
        clearTimeout(searchTimer);
        const q = $("searchInput").value.trim();
        if (q.length < 2) { renderTree(); return; }
        searchTimer = setTimeout(() => runSearch(q), 250);
    });
    $("searchInput").addEventListener("keydown", (e) => {
        if (e.key === "Escape") {
            e.stopPropagation();
            $("searchInput").value = "";
            renderTree();
            $("searchInput").blur();
        }
    });

    $("paletteInput").addEventListener("input", () => {
        clearTimeout(palette.timer);
        const q = $("paletteInput").value.trim();
        if (q.length < 2) { paletteDefault(); return; }
        palette.timer = setTimeout(() => paletteSearch(q), 150);
    });
    $("paletteInput").addEventListener("keydown", (e) => {
        if (e.key === "ArrowDown") { e.preventDefault(); paletteMove(1); }
        if (e.key === "ArrowUp") { e.preventDefault(); paletteMove(-1); }
        if (e.key === "Enter") { e.preventDefault(); paletteOpen(palette.sel); }
        if (e.key === "Escape") { e.stopPropagation(); closePalette(); }
    });
    $("palette").addEventListener("mousedown", (e) => { if (e.target === $("palette")) closePalette(); });

    // find bar
    $("findInput").addEventListener("input", () => {
        clearTimeout(find.timer);
        find.timer = setTimeout(runFind, 120);
    });
    $("findInput").addEventListener("keydown", (e) => {
        if (e.key === "Enter") {
            e.preventDefault();
            clearTimeout(find.timer);
            if (find.lastRun !== findSignature()) runFind();
            else e.shiftKey ? findPrev() : findNext();
        }
        if (e.key === "Escape") { e.stopPropagation(); closeFindBar(); }
    });
    $("findPrev").addEventListener("click", findPrev);
    $("findNext").addEventListener("click", findNext);
    $("findClose").addEventListener("click", closeFindBar);
    $("findCase").addEventListener("click", () => {
        find.caseSensitive = !find.caseSensitive;
        $("findCase").classList.toggle("active", find.caseSensitive);
        runFind(); $("findInput").focus();
    });
    $("findRegex").addEventListener("click", () => {
        find.regex = !find.regex;
        $("findRegex").classList.toggle("active", find.regex);
        runFind(); $("findInput").focus();
    });
    $("findWrap").addEventListener("click", () => {
        find.wrap = !find.wrap;
        $("findWrap").classList.toggle("active", find.wrap);
        $("findInput").focus();
    });

    document.addEventListener("keydown", (e) => {
        if (e.altKey && e.code === "ArrowLeft") { e.preventDefault(); goBack(); return; }
        if (e.altKey && e.code === "ArrowRight") { e.preventDefault(); goForward(); return; }
        if ((e.ctrlKey || e.metaKey) && e.code === "KeyP") {
            if ($("mainScreen").style.display !== "none") { e.preventDefault(); openPalette(); }
            return;
        }
        if ((e.ctrlKey || e.metaKey) && e.code === "KeyL") {
            if ($("mainScreen").style.display !== "none") { e.preventDefault(); lockNow(); }
            return;
        }
        if (e.key === "F5") {
            if ($("mainScreen").style.display !== "none") { e.preventDefault(); syncNow(); }
            return;
        }
        if ((e.ctrlKey || e.metaKey) && e.code === "KeyK") {
            e.preventDefault(); $("searchInput").focus(); $("searchInput").select();
        }
        if ((e.ctrlKey || e.metaKey) && e.code === "KeyF") {
            if ($("mainScreen").style.display !== "none" && $("docViewer").style.display !== "none") {
                e.preventDefault(); openFindBar();
            }
        }
        if (e.key === "F3" && find.open) {
            e.preventDefault();
            e.shiftKey ? findPrev() : findNext();
        }
        if (e.key === "Escape") {
            if (palette.open) { closePalette(); return; }
            if ($("docMenu").style.display !== "none") { closeMenu(); return; }
            if ($("connModal").style.display !== "none") { closeConnModal(); return; }
            if ($("resetModal").style.display !== "none") { closeResetModal(); return; }
            if ($("aboutModal").style.display !== "none") { closeAboutModal(); return; }
            if (find.open) closeFindBar();
        }
        if ((e.ctrlKey || e.metaKey) && (e.code === "Equal" || e.code === "NumpadAdd")) {
            e.preventDefault(); zoomIn(); return;
        }
        if ((e.ctrlKey || e.metaKey) && (e.code === "Minus" || e.code === "NumpadSubtract")) {
            e.preventDefault(); zoomOut(); return;
        }
        if ((e.ctrlKey || e.metaKey) && e.code === "Digit0") {
            e.preventDefault(); zoomReset(); return;
        }
    });
    document.addEventListener("wheel", (e) => {
        if (e.ctrlKey || e.metaKey) {
            e.preventDefault();
            if (e.deltaY < 0) zoomIn();
            else if (e.deltaY > 0) zoomOut();
        }
    }, { passive: false });
    document.addEventListener("mousedown", (e) => {
        if (e.button === 3) { e.preventDefault(); goBack(); }
        else if (e.button === 4) { e.preventDefault(); goForward(); }
    });
    document.addEventListener("auxclick", (e) => {
        if (e.button === 3 || e.button === 4) e.preventDefault();
    });
    ["mousemove", "mousedown", "keydown", "wheel"].forEach((ev) =>
        document.addEventListener(ev, () => {
            if ($("mainScreen").style.display !== "none") resetIdle();
        }, { passive: true }));

    $("docLink").addEventListener("click", (e) => {
        e.preventDefault();
        if (currentDocUrl) window.runtime.BrowserOpenURL(currentDocUrl);
    });
    $("docLinkCopy").addEventListener("click", () => {
        if (currentDocUrl) copySensitive(currentDocUrl, currentDocUrl);
    });
    $("favBtn").addEventListener("click", toggleFav);
    $("menuAbout").addEventListener("click", () => { closeMenu(); showAbout(); });
    $("menuZoomIn").addEventListener("click", () => { closeMenu(); zoomIn(); });
    $("menuZoomOut").addEventListener("click", () => { closeMenu(); zoomOut(); });
    $("zoomIndicator").addEventListener("click", zoomReset);
    $("menuZoomReset").addEventListener("click", () => { closeMenu(); zoomReset(); });
    $("docContent").addEventListener("click", onContentClick);
    $("backBtn").addEventListener("click", goBack);
    $("forwardBtn").addEventListener("click", goForward);
    $("tocBtn").addEventListener("click", () => applyTocOpen(!tocOpen));
}

/* ---------- Запуск / блокировка ---------- */

async function bootstrap(autoUnlock) {
    let st = await go().StorageStatus();
    isPortable = st.portable;
    if (st.locked) {
        lockHasDB = st.hasDB;
        lockPortable = st.portable;
        lockLegacy = st.legacy;
        const creating = !st.hasDB;
        const needPass = !st.legacy;
        $("lockSubtitle").textContent = st.legacy
            ? "База старого формата (без мастер-пароля). Очистите её и создайте заново — документы снова синхронизируются с Outline."
            : creating
            ? "Придумайте мастер-пароль (минимум 8 символов). Сменить его можно будет только очистив базу."
            : "Введите мастер-пароль";
        $("lockPass").style.display = needPass ? "block" : "none";
        $("lockPass2").style.display = (!st.legacy && creating) ? "block" : "none";
        $("lockBtn").textContent = st.legacy ? "Очистить и создать заново" : creating ? "Создать базу" : "Открыть";
        $("lockReset").style.display = (!st.legacy && !creating) ? "block" : "none";
        $("lockError").textContent = "";
        $("setupScreen").style.display = "none";
        $("mainScreen").style.display = "none";
        $("lockScreen").style.display = "flex";
        if (needPass) $("lockPass").focus();
        go().UnlockRetryAfter().then(startGuardCountdown).catch(() => {});
        return;
    }
    $("lockScreen").style.display = "none";
    startGuardCountdown(0);
    const s = await go().GetSettings();
    if (s && s.apiURL && s.apiKey) {
        $("apiUrl").value = s.apiURL;
        $("apiKey").value = s.apiKey;
        enterMain();
    } else {
        $("setupScreen").style.display = "flex";
    }
}

async function unlockDB() {
    if (lockLegacy) {
        const btn = $("lockBtn");
        btn.disabled = true;
        try {
            await go().ResetApp();
            await bootstrap(true);
        } catch (err) {
            $("lockError").textContent = String(err);
        } finally {
            btn.disabled = false;
        }
        return;
    }
    if (guardLeft > 0) return;
    const passVisible = $("lockPass").style.display !== "none";
    const pass = passVisible ? $("lockPass").value : "";
    $("lockError").textContent = "";
    if (!lockHasDB && pass !== $("lockPass2").value) {
        $("lockError").textContent = "Пароли не совпадают";
        return;
    }
    const btn = $("lockBtn");
    btn.disabled = true;
    try {
        await go().UnlockDB(pass);
        startGuardCountdown(0);
        $("lockScreen").style.display = "none";
        $("lockPass").value = ""; $("lockPass2").value = "";
        await bootstrap(true);
    } catch (err) {
        $("lockError").textContent = String(err);
        startGuardCountdown(await go().UnlockRetryAfter());
    } finally {
        btn.disabled = guardLeft > 0;
    }
}

function startGuardCountdown(sec) {
    clearInterval(guardTimer);
    guardLeft = Math.max(0, sec | 0);
    applyGuard();
    if (guardLeft > 0) {
        guardTimer = setInterval(() => {
            guardLeft--;
            applyGuard();
            if (guardLeft <= 0) clearInterval(guardTimer);
        }, 1000);
    }
}
function applyGuard() {
    const blocked = guardLeft > 0;
    $("lockBtn").disabled = blocked;
    $("lockPass").disabled = blocked;
    $("lockPass2").disabled = blocked;
    if (blocked) {
        $("lockError").textContent = "Слишком много неверных попыток. Повтор через " + guardLeft + " с";
    } else if (($("lockError").textContent || "").startsWith("Слишком много")) {
        $("lockError").textContent = "";
    }
}

async function resetFromLock() {
    if (!window.confirm("Удалить локальную базу и мастер-пароль? Документы в Outline не пострадают и синхронизируются заново.")) return;
    const btn = $("lockReset");
    btn.disabled = true;
    try {
        await go().ResetApp();
        await bootstrap(true);
    } catch (err) {
        $("lockError").textContent = String(err);
    } finally {
        btn.disabled = false;
    }
}

async function lockNow() {
    if ($("mainScreen").style.display === "none") return;
    try { await go().LockApp(); } catch (e) {}
    currentOID = null;
    currentDocUpdated = "";
    currentDocUrl = "";
    history = []; historyIndex = -1; updateHistoryButtons();
    $("docContent").innerHTML = ""; $("docTitle").textContent = "";
    $("docDate").textContent = ""; $("docCrumbs").innerHTML = "";
    $("docLink").style.display = "none";
    $("docLinkCopy").style.display = "none";
    $("favBtn").style.display = "none";
    $("docTree").innerHTML = ""; collections = []; index = {}; favorites = [];
    $("searchInput").value = "";
    $("tocPanel").style.display = "none";
    $("tocContent").innerHTML = "";
    closeFindBar(); closeMenu(); closePalette();
    $("docViewer").style.display = "none";
    $("welcomeScreen").style.display = "none";
    $("updateNotification").style.display = "none";
    $("toast").style.display = "none";
    $("mainScreen").style.display = "none";
    if (updateTimer) { clearInterval(updateTimer); updateTimer = null; }
    clearTimeout(idleTimer);
    await bootstrap(false);
}

function resetIdle() {
    clearTimeout(idleTimer);
    idleTimer = setTimeout(lockNow, IDLE_LOCK_MS);
}

/* ---------- Экраны / подключение ---------- */

function enterMain() {
    $("setupScreen").style.display = "none";
    $("lockScreen").style.display = "none";
    $("mainScreen").style.display = "flex";
    loadTree();
    if (updateTimer) clearInterval(updateTimer);
    updateTimer = setInterval(checkUpdates, 60000);
    checkUpdates();
    resetIdle();
    initCapture();
}

async function connect() {
    const url = $("apiUrl").value.trim();
    const key = $("apiKey").value.trim();
    $("setupError").textContent = "";
    if (!url || !key) { $("setupError").textContent = "Заполните URL сервера и API-ключ"; return; }
    const btn = $("connectBtn");
    btn.disabled = true;
    try {
        btn.textContent = "Проверка подключения...";
        await go().SetConnection(url, key);
        btn.textContent = "Синхронизация...";
        await go().SyncDocuments();
        setNet(true);
        enterMain();
    } catch (err) {
        $("setupError").textContent = String(err);
    } finally {
        btn.disabled = false;
        btn.textContent = "Подключиться";
    }
}

async function showAbout() {
    const v = await go().AppVersion();
    $("aboutVersion").textContent = v === "dev" ? "dev-сборка" : "v" + v;
    $("aboutModal").style.display = "flex";
}

function openMenu() {
    const noDoc = $("docViewer").style.display === "none";
    $("menuFind").classList.toggle("disabled", noDoc);
    $("fwSwitch").classList.toggle("on", fullWidth);
    $("capSwitch").classList.toggle("on", captureProtect);
    $("docMenu").style.display = "block";
}

function closeMenu() { $("docMenu").style.display = "none"; }

async function openConnModal() {
    $("connError").textContent = "";
    $("connModal").style.display = "flex";
    try {
        const s = await go().GetSettings();
        $("connUrl").value = (s && s.apiURL) || "";
        $("connKey").value = (s && s.apiKey) || "";
    } catch (err) { /* покажем пустые поля */ }
    $("connUrl").focus();
}

function closeConnModal() { $("connModal").style.display = "none"; }

async function saveConnection() {
    const url = $("connUrl").value.trim();
    const key = $("connKey").value.trim();
    $("connError").textContent = "";
    if (!url || !key) { $("connError").textContent = "Заполните URL сервера и API-ключ"; return; }
    const btn = $("connSave");
    btn.disabled = true;
    try {
        btn.textContent = "Проверка...";
        await go().SetConnection(url, key);
        btn.textContent = "Синхронизация...";
        const res = await go().SyncDocuments();
        setNet(true);
        closeConnModal();
        showToast("Подключение обновлено", "Документов в базе: " + res.total, false);
        await loadTree();
        updateStatus();
        await refreshCurrentDoc();
    } catch (err) {
        $("connError").textContent = String(err);
    } finally {
        btn.disabled = false;
        btn.textContent = "Сохранить";
    }
}

function openResetModal() { $("resetModal").style.display = "flex"; }
function closeResetModal() { $("resetModal").style.display = "none"; }
function closeAboutModal() { $("aboutModal").style.display = "none"; }

async function doReset() {
    const btn = $("resetConfirm");
    btn.disabled = true;
    btn.textContent = "Очистка...";
    try {
        if (updateTimer) { clearInterval(updateTimer); updateTimer = null; }
        clearTimeout(idleTimer);
        clearTimeout(searchTimer);
        await go().ResetApp();
        ["or.expanded", "or.theme", "or.sideWidth", "or.fullWidth", "or.fontScale", "or.tocOpen", "or.captureProtect"]
            .forEach((k) => localStorage.removeItem(k));
        window.location.reload();
    } catch (err) {
        showToast("Ошибка очистки", String(err), true);
        btn.disabled = false;
        btn.textContent = "Очистить";
    }
}

/* ---------- Защита от захвата экрана ---------- */

function initCapture() {
    const saved = localStorage.getItem("or.captureProtect");
    const on = saved === null ? isPortable : saved === "1";
    if (!go().SetCaptureProtection) return;
    go().SetCaptureProtection(on).then((ok) => {
        captureProtect = ok && on;
    }).catch(() => {});
}

function toggleCapture() {
    if (!go().SetCaptureProtection) {
        showToast("Защита от захвата", "Доступна только на Windows", true);
        return;
    }
    const next = !captureProtect;
    go().SetCaptureProtection(next).then((ok) => {
        captureProtect = ok && next;
        localStorage.setItem("or.captureProtect", captureProtect ? "1" : "0");
        $("capSwitch").classList.toggle("on", captureProtect);
        showToast(captureProtect ? "Защита от захвата включена" : "Защита от захвата выключена",
            ok ? "Окно не видно программам записи экрана" : "Не удалось применить (Windows 10 2004+)", !ok);
    }).catch((err) => showToast("Ошибка", String(err), true));
}

/* ---------- Буфер обмена ---------- */

function scheduleClipClear(text) {
    clearTimeout(clipTimer);
    clipTimer = setTimeout(async () => {
        try {
            const cur = await window.runtime.ClipboardGetText();
            if (cur === text) await window.runtime.ClipboardSetText("");
        } catch (e) {}
    }, 30000);
}

async function copySensitive(text, label) {
    const ok = await window.runtime.ClipboardSetText(text);
    showToast(ok ? "Скопировано (очистится через 30 с)" : "Ошибка", label, !ok);
    if (ok) scheduleClipClear(text);
}

/* ---------- Дерево ---------- */

async function loadTree() {
    try {
        collections = await go().GetTree();
        buildIndex();
        await refreshFavorites();
        if (currentOID) expandAncestors(currentOID);
        renderTree();
    } catch (err) {
        $("docTree").innerHTML = "";
        const d = document.createElement("div");
        d.className = "tree-empty";
        d.textContent = "Не удалось загрузить дерево: " + err;
        $("docTree").appendChild(d);
    }
}

function buildIndex() {
    index = {};
    for (const col of collections) {
        const walk = (nodes, parent) => {
            for (const n of nodes || []) {
                index[n.oid] = { title: n.title, parent, colName: col.name };
                walk(n.children, n.oid);
            }
        };
        walk(col.nodes, null);
    }
}

async function updateStatus() {
    try {
        const st = await go().GetSyncStats();
        const el = $("statusText");
        if (!st.lastSync) {
            el.textContent = st.total ? pluralDocs(st.total) : "";
            $("syncDelta").textContent = "";
            $("sideStatus").title = "";
            return;
        }
        const d = new Date(st.lastSync);
        const hm = d.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" });
        const sameDay = d.toDateString() === new Date().toDateString();
        const day = sameDay ? "" : " " + d.toLocaleDateString("ru-RU", { day: "2-digit", month: "2-digit" });
        el.textContent = st.total + " док · " + hm + day;
        $("sideStatus").title = "Последняя синхронизация: " + d.toLocaleString("ru-RU");
        const parts = [];
        if (st.added) parts.push("+" + st.added);
        if (st.updated) parts.push("~" + st.updated);
        if (st.deleted) parts.push("−" + st.deleted);
        $("syncDelta").textContent = parts.join(" ");
        $("syncDelta").title = "Новых: " + st.added + ", обновлено: " + st.updated + ", удалено: " + st.deleted;
    } catch (e) {}
}

function pluralDocs(n) {
    const m10 = n % 10, m100 = n % 100;
    if (m10 === 1 && m100 !== 11) return n + " документ";
    if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return n + " документа";
    return n + " документов";
}

function saveExpanded() { localStorage.setItem("or.expanded", JSON.stringify([...expanded])); }

function expandAncestors(oid) {
    let p = (index[oid] || {}).parent;
    while (p) { expanded.add(p); p = (index[p] || {}).parent; }
    saveExpanded();
}

function renderTree() {
    const el = $("docTree");
    el.innerHTML = "";
    if (!collections.length) {
        const d = document.createElement("div");
        d.className = "tree-empty";
        d.textContent = "База пуста. Нажмите кнопку синхронизации в меню.";
        el.appendChild(d);
        return;
    }
    if (favorites.length) {
        const sec = document.createElement("section");
        const h = document.createElement("div");
        h.className = "collection-title";
        h.textContent = "Избранное";
        sec.appendChild(h);
        const ul = document.createElement("ul");
        ul.className = "tree-list";
        for (const d of favorites) {
            const li = document.createElement("li");
            const row = document.createElement("div");
            row.className = "tree-row fav-row";
            row.dataset.oid = d.OutlineID;
            row.style.paddingLeft = "8px";
            const star = document.createElement("span");
            star.className = "fav-star";
            star.innerHTML = ICON_STAR;
            const label = document.createElement("span");
            label.className = "tree-label";
            label.textContent = d.Title || "Без названия";
            row.append(star, label);
            row.addEventListener("click", () => openDocument(d.OutlineID, undefined, "fav"));
            li.appendChild(row);
            ul.appendChild(li);
        }
        sec.appendChild(ul);
        el.appendChild(sec);
    }
    for (const col of collections) {
        const sec = document.createElement("section");
        const h = document.createElement("div");
        h.className = "collection-title";
        h.textContent = col.name;
        sec.appendChild(h);
        sec.appendChild(renderNodes(col.nodes, 0));
        el.appendChild(sec);
    }
    updateStatus();
    markActive();
}

function toggleNode(oid) {
    expanded.has(oid) ? expanded.delete(oid) : expanded.add(oid);
    saveExpanded();
    const sidebar = document.querySelector(".sidebar");
    const scroll = sidebar.scrollTop;
    renderTree();
    sidebar.scrollTop = scroll;
}

function renderNodes(nodes, depth) {
    const ul = document.createElement("ul");
    ul.className = "tree-list";
    for (const n of nodes || []) {
        const kids = n.children || [];
        const li = document.createElement("li");
        const row = document.createElement("div");
        row.className = "tree-row";
        row.dataset.oid = n.oid;
        row.style.paddingLeft = (8 + depth * 14) + "px";
        if (kids.length) row.title = "Двойной клик — развернуть/свернуть";

        const chev = document.createElement("button");
        chev.className = "chev" + (kids.length ? (expanded.has(n.oid) ? " open" : "") : " leaf");
        chev.tabIndex = -1;
        chev.addEventListener("click", (e) => {
            e.stopPropagation();
            if (kids.length) toggleNode(n.oid);
        });

        const label = document.createElement("span");
        label.className = "tree-label";
        label.textContent = n.title || "Без названия";

        row.append(chev, label);
        row.addEventListener("click", () => {
            const now = Date.now();
            if (kids.length && lastTreeClick.oid === n.oid && now - lastTreeClick.time < 400) {
                lastTreeClick = { oid: null, time: 0 };
                toggleNode(n.oid);
                return;
            }
            lastTreeClick = { oid: n.oid, time: now };
            openDocument(n.oid);
        });
        li.appendChild(row);
        if (kids.length && expanded.has(n.oid)) li.appendChild(renderNodes(kids, depth + 1));
        ul.appendChild(li);
    }
    return ul;
}

function markActive() {
    document.querySelectorAll(".tree-row.active").forEach((el) => el.classList.remove("active"));
    if (!currentOID) return;
    const esc = CSS.escape(currentOID);
    let row = activeSource === "fav"
        ? document.querySelector(`.tree-row.fav-row[data-oid="${esc}"]`)
        : document.querySelector(`.tree-row:not(.fav-row)[data-oid="${esc}"]`);
    if (!row) row = document.querySelector(`.tree-row[data-oid="${esc}"]`);
    if (row) { row.classList.add("active"); row.scrollIntoView({ block: "nearest" }); }
}

function goBack() {
    if (historyIndex <= 0) return;
    historyIndex--;
    skipHistoryPush = true;
    openDocument(history[historyIndex]);
}

function goForward() {
    if (historyIndex >= history.length - 1) return;
    historyIndex++;
    skipHistoryPush = true;
    openDocument(history[historyIndex]);
}

function updateHistoryButtons() {
    $("backBtn").disabled = historyIndex <= 0;
    $("forwardBtn").disabled = historyIndex >= history.length - 1;
}

/* ---------- Избранное / недавние / палитра ---------- */

async function refreshFavorites() {
    try { favorites = (await go().GetFavorites()) || []; } catch (e) { favorites = []; }
}

async function toggleFav() {
    if (!currentOID) return;
    const on = await go().ToggleFavorite(currentOID);
    updateFavBtn(on);
    await refreshFavorites();
    renderTree();
    showToast(on ? "Добавлено в избранное" : "Убрано из избранного", $("docTitle").textContent, false);
}

function updateFavBtn(on) {
    const b = $("favBtn");
    b.style.display = "grid";
    b.classList.toggle("active", !!on);
    b.title = on ? "Убрать из избранного" : "В избранное";
}

function openPalette() {
    palette.open = true;
    $("palette").style.display = "flex";
    $("paletteInput").value = "";
    $("paletteInput").focus();
    paletteDefault();
}

function closePalette() {
    palette.open = false;
    $("palette").style.display = "none";
}

async function paletteDefault() {
    try {
        const [fav, rec] = await Promise.all([go().GetFavorites(), go().GetRecent()]);
        const seen = new Set();
        palette.items = [];
        for (const d of fav || []) { seen.add(d.OutlineID); palette.items.push({ doc: d, fav: true }); }
        for (const d of rec || []) { if (!seen.has(d.OutlineID)) palette.items.push({ doc: d, fav: false }); }
        palette.sel = 0;
        renderPalette("");
    } catch (e) {}
}

async function paletteSearch(q) {
    try {
        const res = await go().SearchDocuments(q);
        if ($("paletteInput").value.trim() !== q) return;
        palette.items = (res || []).slice(0, 12).map((d) => ({ doc: d, fav: false }));
        palette.sel = 0;
        renderPalette(q);
    } catch (e) {}
}

function renderPalette(q) {
    const list = $("paletteList");
    list.innerHTML = "";
    if (!palette.items.length) {
        const d = document.createElement("div");
        d.className = "palette-empty";
        d.textContent = q ? "Ничего не найдено" : "Нет недавних документов";
        list.appendChild(d);
        return;
    }
    palette.items.forEach((it, i) => {
        const row = document.createElement("div");
        row.className = "palette-item" + (i === palette.sel ? " sel" : "");
        const t = document.createElement("div");
        t.className = "palette-title";
        highlightInto(t, it.doc.Title || "Без названия", q);
        const meta = document.createElement("div");
        meta.className = "palette-meta";
        meta.textContent = (index[it.doc.OutlineID] || {}).colName || "";
        if (it.fav) {
            const s = document.createElement("span");
            s.className = "palette-fav";
            s.textContent = "избранное";
            meta.appendChild(s);
        }
        row.append(t, meta);
        row.addEventListener("click", () => paletteOpen(i));
        list.appendChild(row);
    });
}

function paletteOpen(i) {
    const it = palette.items[i];
    if (!it) return;
    closePalette();
    openDocument(it.doc.OutlineID);
}

function paletteMove(dir) {
    const n = palette.items.length;
    if (!n) return;
    palette.sel = (palette.sel + dir + n) % n;
    renderPalette($("paletteInput").value.trim());
    const el = $("paletteList").children[palette.sel];
    if (el) el.scrollIntoView({ block: "nearest" });
}

/* ---------- Документ ---------- */

async function openDocument(oid, query, source) {
    activeSource = source === "fav" ? "fav" : "tree";
    try {
        const doc = await go().GetDocument(oid);
        const s = await go().GetSettings().catch(() => null);

        if (!skipHistoryPush) {
            if (historyIndex < history.length - 1) {
                history = history.slice(0, historyIndex + 1);
            }
            if (history[historyIndex] !== oid) {
                history.push(oid);
                historyIndex++;
            }
        }
        skipHistoryPush = false;
        updateHistoryButtons();

        currentOID = oid;
        expandAncestors(oid);
        renderTree();
        showDoc(doc, (s && s.apiURL) || "");
        go().IsFavorite(oid).then(updateFavBtn).catch(() => {});
        if (query) {
            openFindBar(query);
        } else if (find.open) {
            runFind();
        } else {
            find.matches = [];
            find.current = -1;
        }
    } catch (err) {
        showToast("Ошибка", "Не удалось открыть документ: " + err, true);
    }
}

function showDoc(doc, base) {
    $("welcomeScreen").style.display = "none";
    $("docViewer").style.display = "block";
    $("docTitle").textContent = doc.Title || "Без названия";
    $("docDate").textContent = doc.UpdatedAt ? "Обновлено: " + new Date(doc.UpdatedAt).toLocaleString("ru-RU") : "";
    currentDocUpdated = doc.UpdatedAt || "";
    renderDocLink(doc, base);
    renderCrumbs(doc.OutlineID);
    renderMarkdown(doc.Text || "");
    renderTOC();
    document.querySelector(".viewer").scrollTop = 0;
}

async function refreshCurrentDoc() {
    if (!currentOID) return;
    let doc;
    try {
        doc = await go().GetDocument(currentOID);
    } catch (e) {
        showToast("Документ удалён", "Документ больше не существует на сервере", true);
        return;
    }
    if ((doc.UpdatedAt || "") === currentDocUpdated) return;
    const viewer = document.querySelector(".viewer");
    const scroll = viewer.scrollTop;
    const s = await go().GetSettings().catch(() => null);
    showDoc(doc, (s && s.apiURL) || "");
    viewer.scrollTop = scroll;
    if (find.open) runFind();
    showToast("Документ обновлён", doc.Title || "", false);
}

function renderDocLink(doc, base) {
    const path = doc.Url || (doc.UrlID ? "/doc/" + doc.UrlID : "");
    currentDocUrl = base && path ? base.replace(/\/+$/, "") + path : "";
    const link = $("docLink"), copy = $("docLinkCopy");
    if (!currentDocUrl) {
        link.style.display = "none";
        copy.style.display = "none";
        return;
    }
    link.style.display = "inline-flex";
    copy.style.display = "grid";
    link.href = currentDocUrl;
    link.title = "Открыть в Outline: " + currentDocUrl;
    $("docLinkText").textContent = currentDocUrl.replace(/^https?:\/\//, "");
}

function renderCrumbs(oid) {
    const el = $("docCrumbs");
    el.innerHTML = "";
    const info = index[oid];
    if (!info) return;
    const chain = [];
    let p = info.parent;
    while (p) { chain.unshift(p); p = (index[p] || {}).parent; }
    const parts = [{ title: info.colName }].concat(
        chain.map((id) => ({ oid: id, title: (index[id] || {}).title || "..." }))
    );
    parts.forEach((part, i) => {
        if (i > 0) {
            const s = document.createElement("span");
            s.className = "crumb-sep"; s.textContent = "/";
            el.appendChild(s);
        }
        if (part.oid) {
            const a = document.createElement("a");
            a.textContent = part.title;
            a.addEventListener("click", () => openDocument(part.oid));
            el.appendChild(a);
        } else {
            const s = document.createElement("span");
            s.textContent = part.title;
            el.appendChild(s);
        }
    });
}

function renderMarkdown(md) {
    const el = $("docContent");
    el.innerHTML = DOMPurify.sanitize(transformNotices(md, 0), {
        ADD_TAGS: ["input"],
        ADD_ATTR: ["type", "checked", "disabled"],
    });
    applyInlineMarks(el);
    el.querySelectorAll("img").forEach((img) => {
        const src = img.getAttribute("src") || "";
        if (src.includes("attachments.redirect")) {
            const id = new URL(src, "http://local").searchParams.get("id");
            if (id) img.src = "/img?id=" + encodeURIComponent(id);
        }
        img.loading = "lazy";
        img.addEventListener("error", () => {
            img.classList.add("img-missing");
            img.alt = "Вложение не закэшировано — подключитесь к сети и синхронизируйте";
        });
    });
    el.querySelectorAll("table").forEach((t) => {
        const wrap = document.createElement("div");
        wrap.className = "table-wrap";
        t.replaceWith(wrap); wrap.appendChild(t);
    });
    el.querySelectorAll("pre").forEach((pre) => {
        const wrap = document.createElement("div");
        wrap.className = "code-wrap";
        pre.replaceWith(wrap); wrap.appendChild(pre);
        const btn = document.createElement("button");
        btn.className = "copy-btn";
        btn.textContent = "Копировать";
        btn.addEventListener("click", async () => {
            const text = pre.innerText;
            const ok = await window.runtime.ClipboardSetText(text);
            btn.textContent = ok ? "Скопировано" : "Ошибка";
            if (ok) scheduleClipClear(text);
            setTimeout(() => { btn.textContent = "Копировать"; }, 1500);
        });
        wrap.appendChild(btn);
    });
}

function transformNotices(md, depth) {
    if (depth > 4) return marked.parse(md);
    const lines = md.split("\n");
    const parts = [];
    let plain = [];
    let fence = null;
    let i = 0;
    const flush = () => {
        if (plain.length) { parts.push(marked.parse(plain.join("\n"))); plain = []; }
    };
    while (i < lines.length) {
        const line = lines[i];
        const fm = line.match(/^\s*(```+|~~~+)/);
        if (fm) {
            if (fence) fence = null; else fence = fm[1];
            plain.push(line); i++; continue;
        }
        const om = !fence ? line.match(/^:::\s*(info|tip|warning|danger|success)\s*$/) : null;
        if (om) {
            flush();
            const kind = om[1];
            const inner = [];
            let innerFence = null;
            i++;
            while (i < lines.length) {
                const l = lines[i];
                const f2 = l.match(/^\s*(```+|~~~+)/);
                if (f2) { innerFence = innerFence ? null : f2[1]; inner.push(l); i++; continue; }
                if (!innerFence && /^:::\s*$/.test(l)) { i++; break; }
                inner.push(l); i++;
            }
            parts.push('<div class="notice notice-' + kind + '">' + transformNotices(inner.join("\n"), depth + 1) + "</div>");
            continue;
        }
        plain.push(line); i++;
    }
    flush();
    return parts.join("\n");
}

function applyInlineMarks(root) {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
        acceptNode(n) {
            if (!n.nodeValue || (!n.nodeValue.includes("==") && !n.nodeValue.includes("++"))) return NodeFilter.FILTER_REJECT;
            const p = n.parentElement;
            if (!p || p.closest("pre, code, script, style")) return NodeFilter.FILTER_REJECT;
            return NodeFilter.FILTER_ACCEPT;
        },
    });
    const nodes = [];
    while (walker.nextNode()) nodes.push(walker.currentNode);
    const re = /==(?=\S)([^=]+?)(?<=\S)==|\+\+(?=\S)([^+]+?)(?<=\S)\+\+/g;
    for (const node of nodes) {
        const text = node.nodeValue;
        re.lastIndex = 0;
        if (!re.test(text)) continue;
        re.lastIndex = 0;
        const frag = document.createDocumentFragment();
        let last = 0, m;
        while ((m = re.exec(text)) !== null) {
            if (m.index > last) frag.appendChild(document.createTextNode(text.slice(last, m.index)));
            const el = document.createElement(m[1] !== undefined ? "mark" : "u");
            el.textContent = m[1] !== undefined ? m[1] : m[2];
            frag.appendChild(el);
            last = m.index + m[0].length;
        }
        if (last < text.length) frag.appendChild(document.createTextNode(text.slice(last)));
        node.parentNode.replaceChild(frag, node);
    }
}

function renderTOC() {
    const panel = $("tocPanel");
    const content = $("tocContent");
    const btn = $("tocBtn");
    const headings = $("docContent").querySelectorAll("h1, h2, h3");

    content.innerHTML = "";
    if (headings.length > 0) {
        btn.disabled = false;
        headings.forEach((h, i) => {
            const id = "toc-heading-" + i;
            h.id = id;
            const item = document.createElement("a");
            item.className = "toc-item toc-" + h.tagName.toLowerCase();
            item.textContent = h.textContent;
            item.href = "#" + id;
            item.addEventListener("click", (e) => {
                e.preventDefault();
                h.scrollIntoView({ behavior: "smooth", block: "start" });
            });
            content.appendChild(item);
        });
    } else {
        btn.disabled = true;
    }
    applyTocOpen(tocOpen);
}

function applyTocOpen(v) {
    tocOpen = !!v;
    localStorage.setItem("or.tocOpen", tocOpen ? "1" : "0");
    const hasItems = $("tocContent").children.length > 0;
    const show = tocOpen && hasItems;
    $("tocPanel").style.display = show ? "block" : "none";
    $("tocBtn").classList.toggle("active", show);
    $("docViewer").classList.toggle("toc-open", show);
}

async function onContentClick(e) {
    const code = e.target.closest("code");
    if (code && !code.closest("pre")) {
        const text = code.textContent;
        if (!text) return;
        copySensitive(text, text.slice(0, 80));
        return;
    }

    const a = e.target.closest("a");
    if (!a) return;
    let href = a.getAttribute("href") || "";

    if (href.startsWith("http")) {
        try {
            const urlObj = new URL(href);
            href = urlObj.pathname + urlObj.search + urlObj.hash;
        } catch (err) { }
    }

    if (href.startsWith("/doc/")) {
        e.preventDefault();
        const path = href.split(/[?#]/)[0].replace(/\/+$/, "");
        const slug = path.replace("/doc/", "");

        const candidates = new Set();
        candidates.add(slug);

        if (slug.includes("-")) {
            const parts = slug.split("-");
            candidates.add(parts[parts.length - 1]);
        }

        let found = false;
        for (const candidate of candidates) {
            try {
                const d = await go().GetDocumentByURLID(candidate);
                if (d && d.OutlineID) {
                    openDocument(d.OutlineID);
                    found = true;
                    break;
                }
            } catch (err) { }
        }

        if (!found) {
            showToast("Документ не найден", "Выполните синхронизацию или внутренняя ошибка", true);
        }
        return;
    }

    if (href.startsWith("/img?") || href.includes("attachments.redirect")) {
        e.preventDefault();
        let url = href;
        if (href.includes("attachments.redirect")) {
            const id = new URL(href, "http://local").searchParams.get("id");
            if (!id) return;
            url = "/img?id=" + encodeURIComponent(id);
        }
        try {
            const r = await fetch(url);
            if (!r.ok) throw new Error("HTTP " + r.status);
            const blob = await r.blob();
            const u = URL.createObjectURL(blob);
            const t = document.createElement("a");
            t.href = u;
            t.download = (a.textContent || "file").trim() || "file";
            document.body.appendChild(t); t.click(); t.remove();
            setTimeout(() => URL.revokeObjectURL(u), 5000);
        } catch (err) {
            showToast("Файл", "Не удалось скачать: " + err, true);
        }
        return;
    }

    if (/^https?:/i.test(href)) {
        e.preventDefault();
        window.runtime.BrowserOpenURL(href);
    }
}

/* ---------- Глобальный поиск ---------- */

async function runSearch(q) {
    try {
        const results = await go().SearchDocuments(q);
        if ($("searchInput").value.trim() !== q) return;
        const tree = $("docTree");
        tree.innerHTML = "";
        if (!results || !results.length) {
            const d = document.createElement("div");
            d.className = "tree-empty"; d.textContent = "Ничего не найдено";
            tree.appendChild(d);
            return;
        }
        for (const doc of results) {
            const item = document.createElement("div");
            item.className = "search-hit";
            const t = document.createElement("div");
            t.className = "hit-title";
            highlightInto(t, doc.Title || "Без названия", q);
            const s = document.createElement("div");
            s.className = "hit-snippet";
            highlightInto(s, snippet(doc.Text || "", q), q);
            item.append(t, s);
            item.addEventListener("click", () => openDocument(doc.OutlineID, q));
            tree.appendChild(item);
        }
    } catch (err) {
        console.error("[UI] search:", err);
    }
}

function snippet(text, q) {
    const i = text.toLowerCase().indexOf(q.toLowerCase());
    if (i < 0) return text.slice(0, 120).replace(/\n+/g, " ");
    const from = Math.max(0, i - 50);
    return (from > 0 ? "..." : "") + text.slice(from, i + q.length + 90).replace(/\n+/g, " ") + "...";
}

function highlightInto(el, text, q) {
    el.textContent = "";
    if (!q) { el.textContent = text; return; }
    const lower = text.toLowerCase(), ql = q.toLowerCase();
    let i = 0, pos;
    while ((pos = lower.indexOf(ql, i)) !== -1) {
        if (pos > i) el.appendChild(document.createTextNode(text.slice(i, pos)));
        const mark = document.createElement("mark");
        mark.className = "hl";
        mark.textContent = text.slice(pos, pos + q.length);
        el.appendChild(mark);
        i = pos + q.length;
    }
    if (i < text.length) el.appendChild(document.createTextNode(text.slice(i)));
}

/* ---------- Поиск в документе ---------- */

function findRoots() { return [$("docTitle"), $("docContent")]; }

function findSignature() {
    return $("findInput").value + "\u0000" + find.caseSensitive + "\u0000" + find.regex;
}

function openFindBar(prefill) {
    find.open = true;
    $("findBar").style.display = "flex";
    if (prefill !== undefined) $("findInput").value = prefill;
    $("findInput").focus();
    $("findInput").select();
    runFind();
}

function closeFindBar() {
    find.open = false;
    $("findBar").style.display = "none";
    clearHighlights();
    find.matches = [];
    find.current = -1;
    updateFindCount();
}

function clearHighlights() {
    for (const root of findRoots()) {
        root.querySelectorAll("mark.find-hit").forEach((m) => {
            const parent = m.parentNode;
            parent.replaceChild(document.createTextNode(m.textContent), m);
            parent.normalize();
        });
    }
}

function buildRegex(q) {
    const src = find.regex ? q : q.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    return new RegExp(src, find.caseSensitive ? "g" : "gi");
}

function runFind() {
    clearHighlights();
    find.matches = [];
    find.current = -1;
    find.lastRun = findSignature();
    const input = $("findInput");
    input.classList.remove("invalid");
    const q = input.value;
    if (!q || $("docViewer").style.display === "none") { updateFindCount(); return; }

    let re;
    try { re = buildRegex(q); }
    catch (err) { input.classList.add("invalid"); updateFindCount(); return; }

    for (const root of findRoots()) {
        const nodes = [];
        const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
            acceptNode(n) {
                if (!n.nodeValue || !n.nodeValue.trim()) return NodeFilter.FILTER_REJECT;
                const p = n.parentElement;
                if (!p || p.closest(".copy-btn, script, style")) return NodeFilter.FILTER_REJECT;
                return NodeFilter.FILTER_ACCEPT;
            },
        });
        while (walker.nextNode()) nodes.push(walker.currentNode);

        for (const node of nodes) {
            if (find.matches.length >= FIND_LIMIT) break;
            const text = node.nodeValue;
            const ranges = [];
            re.lastIndex = 0;
            let m;
            while ((m = re.exec(text)) !== null) {
                if (m[0] === "") { re.lastIndex++; continue; }
                ranges.push([m.index, m.index + m[0].length]);
                if (find.matches.length + ranges.length >= FIND_LIMIT) break;
            }
            if (!ranges.length) continue;

            const frag = document.createDocumentFragment();
            let last = 0;
            for (const [s, e] of ranges) {
                if (s > last) frag.appendChild(document.createTextNode(text.slice(last, s)));
                const mark = document.createElement("mark");
                mark.className = "find-hit";
                mark.textContent = text.slice(s, e);
                frag.appendChild(mark);
                find.matches.push(mark);
                last = e;
            }
            if (last < text.length) frag.appendChild(document.createTextNode(text.slice(last)));
            node.parentNode.replaceChild(frag, node);
        }
    }

    if (find.matches.length) {
        find.current = 0;
        find.matches[0].classList.add("current");
        find.matches[0].scrollIntoView({ block: "center", behavior: "smooth" });
    }
    updateFindCount();
}

function updateFindCount() {
    const n = find.matches.length;
    $("findCount").textContent = n ? (find.current + 1) + "/" + n : "0/0";
    $("findPrev").disabled = !n;
    $("findNext").disabled = !n;
}

function stepFind(dir) {
    const n = find.matches.length;
    if (!n) return;
    let i = find.current + dir;
    if (find.wrap) i = (i + n) % n;
    else i = Math.max(0, Math.min(n - 1, i));
    if (find.current >= 0 && find.matches[find.current]) {
        find.matches[find.current].classList.remove("current");
    }
    find.current = i;
    find.matches[i].classList.add("current");
    find.matches[i].scrollIntoView({ block: "center", behavior: "smooth" });
    updateFindCount();
}

function findNext() { stepFind(1); }
function findPrev() { stepFind(-1); }

/* ---------- Синхронизация / обновления / статус ---------- */

function fmtDelta(res) {
    const p = [];
    if (res.added) p.push("+" + res.added + " новых");
    if (res.updated) p.push(res.updated + " обновлено");
    if (res.deleted) p.push(res.deleted + " удалено");
    return p.length ? p.join(", ") : "без изменений";
}

async function syncNow() {
    const btn = $("syncBtn");
    btn.classList.add("spin"); btn.disabled = true;
    try {
        const res = await go().SyncDocuments();
        setNet(true);
        showToast("Синхронизация завершена", "Документов: " + res.total + " · " + fmtDelta(res), false);
        await loadTree();
        updateStatus();
        await refreshCurrentDoc();
    } catch (err) {
        setNet(false);
        showToast("Ошибка синхронизации", String(err), true);
    } finally {
        btn.classList.remove("spin"); btn.disabled = false;
    }
}

async function checkUpdates() {
    try {
        const titles = await go().CheckForUpdates();
        setNet(true);
        if (titles && titles.length) {
            const list = $("updatedDocs");
            list.innerHTML = "";
            titles.forEach((t) => {
                const li = document.createElement("li");
                li.textContent = t;
                list.appendChild(li);
            });
            $("updateNotification").style.display = "block";
            setTimeout(() => { $("updateNotification").style.display = "none"; }, 6000);
            await loadTree();
            updateStatus();
            await refreshCurrentDoc();
        }
    } catch (err) {
        setNet(false);
        console.error("[UI] update check:", err);
    }
}

function setNet(online) {
    const el = $("netStatus");
    if (online === null) {
        el.style.display = "none";
        el.className = "net-dot";
        el.title = "";
        return;
    }
    el.style.display = "inline-block";
    el.classList.toggle("online", online);
    el.classList.toggle("offline", !online);
    el.title = online ? "Связь с сервером есть" : "Нет связи с сервером — работает офлайн";
}

function showToast(title, msg, isErr) {
    const t = $("toast");
    t.classList.toggle("error", !!isErr);
    $("toastTitle").textContent = title;
    $("toastMsg").textContent = msg;
    t.style.display = "block";
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { t.style.display = "none"; }, 4000);
}

/* ---------- Тема / ширина / сайдбар ---------- */

function applyTheme(t) {
    document.documentElement.dataset.theme = t;
    localStorage.setItem("or.theme", t);
    $("themeBtn").innerHTML = t === "dark" ? ICON_SUN : ICON_MOON;
    $("themeBtn").title = t === "dark" ? "Светлая тема" : "Тёмная тема";
}

function applyFullWidth(v) {
    fullWidth = v;
    localStorage.setItem("or.fullWidth", v ? "1" : "0");
    $("docViewer").classList.toggle("full-width", v);
    $("fwSwitch").classList.toggle("on", v);
}

function applyFontScale(v) {
    fontScale = Math.round(v * 10) / 10;
    fontScale = Math.max(0.7, Math.min(2.0, fontScale));
    localStorage.setItem("or.fontScale", String(fontScale));
    document.documentElement.style.setProperty("--font-scale", String(fontScale));
    updateZoomIndicator();
}

function zoomIn()    { applyFontScale(fontScale + 0.1); }
function zoomOut()   { applyFontScale(fontScale - 0.1); }
function zoomReset() { applyFontScale(1); }

function updateZoomIndicator() {
    const el = $("zoomIndicator");
    if (!el) return;
    const pct = Math.round(fontScale * 100);
    if (pct === 100) {
        el.style.display = "none";
    } else {
        el.textContent = pct + "%";
        el.style.display = "inline-flex";
    }
}

function initResizer() {
    const sidebar = document.querySelector(".sidebar");
    const saved = parseInt(localStorage.getItem("or.sideWidth") || "300", 10);
    if (saved >= 220 && saved <= 480) sidebar.style.width = saved + "px";
    let dragging = false;
    $("sidebarResizer").addEventListener("mousedown", (e) => {
        dragging = true;
        document.body.classList.add("resizing");
        e.preventDefault();
    });
    document.addEventListener("mousemove", (e) => {
        if (!dragging) return;
        sidebar.style.width = Math.max(220, Math.min(480, e.clientX)) + "px";
    });
    document.addEventListener("mouseup", () => {
        if (!dragging) return;
        dragging = false;
        document.body.classList.remove("resizing");
        localStorage.setItem("or.sideWidth", parseInt(sidebar.style.width, 10) || 300);
    });
}