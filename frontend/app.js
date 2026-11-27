"use strict";

const $ = (id) => document.getElementById(id);
const go = () => window.go.main.App;

const ICON_SUN = '<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>';
const ICON_MOON = '<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/></svg>';
const ICON_STAR = '<svg width="11" height="11" viewBox="0 0 24 24" fill="currentColor" stroke="none"><path d="m12 2 3.1 6.3 6.9 1-5 4.9 1.2 6.8-6.2-3.2-6.2 3.2L7 14.2 2 9.3l6.9-1z"/></svg>';

let collections = [];
let index = {};
let expanded = new Set();
let collapsedCollections = new Set();
let favoritesCollapsed = false;
let history = [];
let historyIndex = -1;
const tabHistories = new Map();
const tabScrollPositions = new Map();
let skipHistoryPush = false;
let currentOID = null;
let openTabs = [];
let pinnedTabs = [];
let activeTabOID = null;
let emptyTabSequence = 0;
const EMPTY_TAB_PREFIX = "__outline_reader_empty_tab__";
const DUPLICATE_TAB_PREFIX = "__outline_reader_duplicate_tab__";
let tabsRestored = false;
let openRequestID = 0;
let draggedTabOID = null;
let contextMenuElement = null;
let currentDocUpdated = "";
let searchTimer = null;
let toastTimer = null, noticeTimer = null;
let toastAction = null;
let toastDuration = 4000;
let updateCheckStarted = false;
const PREF_KEYS = ["theme", "fullWidth", "fontScale", "sideWidth", "sidebarAutoHide", "expanded", "collapsedCollections", "favoritesCollapsed", "tocOpen", "captureProtect", "openTabs", "pinnedTabs", "activeTabOID", "splitView", "splitDocumentOID"];
let prefsReady = false;
let prefsTimer = null;
const prefsDirty = new Map();

function prefGet(key, fallback) {
    const v = localStorage.getItem("or." + key);
    if (v !== null) return v;
    return fallback === undefined ? null : fallback;
}

function prefSet(key, value) {
    const v = String(value);
    localStorage.setItem("or." + key, v);
    if (!prefsReady) return;
    prefsDirty.set(key, v);
    clearTimeout(prefsTimer);
    prefsTimer = setTimeout(flushPrefs, 400);
}

async function flushPrefs() {
    clearTimeout(prefsTimer);
    if (!prefsReady || prefsDirty.size === 0) return;
    const batch = Object.fromEntries(prefsDirty);
    prefsDirty.clear();
    try {
        await go().SetPrefs(batch);
    } catch (err) {
        console.warn("[UI] prefs save:", err);
    }
}

async function loadPrefs() {
    prefsReady = false;
    let remote = {};
    try {
        remote = (await go().GetPrefs()) || {};
    } catch (err) {
        console.warn("[UI] prefs load:", err);
    }
    PREF_KEYS.forEach((k) => {
        if (typeof remote[k] === "string") localStorage.setItem("or." + k, remote[k]);
    });
    applyTheme(prefGet("theme", "dark"));
    applyFullWidth(prefGet("fullWidth") === "1");
    let scale = parseFloat(prefGet("fontScale", "1"));
    if (!(scale >= 0.7 && scale <= 2.0)) scale = 1;
    applyFontScale(scale);
    tocOpen = prefGet("tocOpen") === "1";
    splitViewEnabled = prefGet("splitView") === "1";
    splitDocumentOID = prefGet("splitDocumentOID", "") || null;
    try { expanded = new Set(JSON.parse(prefGet("expanded", "[]"))); } catch (e) { expanded = new Set(); }
    try { collapsedCollections = new Set(JSON.parse(prefGet("collapsedCollections", "[]"))); } catch (e) { collapsedCollections = new Set(); }
    favoritesCollapsed = prefGet("favoritesCollapsed", "false") === "true";
    try { openTabs = JSON.parse(prefGet("openTabs", "[]")); } catch (e) { openTabs = []; }
    if (!Array.isArray(openTabs)) openTabs = [];
    openTabs = [...new Set(openTabs.filter((id) => typeof id === "string" && id))];
    try { pinnedTabs = JSON.parse(prefGet("pinnedTabs", "[]")); } catch (e) { pinnedTabs = []; }
    if (!Array.isArray(pinnedTabs)) pinnedTabs = [];
    pinnedTabs = [...new Set(pinnedTabs.filter((id) => typeof id === "string" && openTabs.includes(id)))];
    normalizeTabOrder();
    activeTabOID = prefGet("activeTabOID", "") || null;
    if (activeTabOID && !openTabs.includes(activeTabOID)) activeTabOID = openTabs[openTabs.length - 1] || null;
    tabsRestored = false;
    const w = parseInt(prefGet("sideWidth", "300"), 10);
    const sidebar = document.querySelector(".sidebar");
    if (sidebar && w >= 220 && w <= 480) {
        sidebar.style.width = w + "px";
        setSideWidth(w);
    }
    prefsReady = true;
    if (Object.keys(remote).length === 0) {
        PREF_KEYS.forEach((k) => {
            const v = localStorage.getItem("or." + k);
            if (v !== null) prefsDirty.set(k, v);
        });
        await flushPrefs();
    }
}

let fullWidth = prefGet("fullWidth") === "1";
let tocOpen = prefGet("tocOpen") === "1";
let fontScale = parseFloat(prefGet("fontScale", "1"));
let lastTreeClick = { oid: null, time: 0 };
let currentDocUrl = "";
let lockHasDB = false, lockPortable = false, lockLegacy = false;
let isPortable = false, captureProtect = false;
let favorites = [];
let activeSource = "tree";
let clipTimer = null;
let syncing = false;
let guardTimer = null, guardLeft = 0;
let cachedApiURL = "";
let connHasKey = false;
let lastActivitySent = 0;
let settingsOpen = false;
let captureBusy = false;
let splitViewEnabled = prefGet("splitView") === "1";
let splitDocumentOID = prefGet("splitDocumentOID", "") || null;
let splitDocumentUpdated = "";

const palette = { open: false, items: [], sel: 0, timer: null };

const find = {
    open: false, matches: [], current: -1,
    caseSensitive: false, regex: false, wrap: true,
    timer: null, lastRun: "",
};
const FIND_LIMIT = 10000;

try { expanded = new Set(JSON.parse(prefGet("expanded", "[]"))); } catch (e) { }
try { collapsedCollections = new Set(JSON.parse(prefGet("collapsedCollections", "[]"))); } catch (e) { }
favoritesCollapsed = prefGet("favoritesCollapsed", "false") === "true";
if (!(fontScale >= 0.7 && fontScale <= 2.0)) fontScale = 1;

document.addEventListener("DOMContentLoaded", async () => {
    applyTheme(prefGet("theme", "dark"));
    applyFullWidth(fullWidth);
    applyFontScale(fontScale);
    bindUI();
    initResizer();
    updateMaxState();
    try {
        await waitBindings();
        bindBackendEvents();
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
    $("sideSyncBtn").addEventListener("click", syncNow);
    $("winMin").addEventListener("click", () => window.runtime.WindowMinimise());
    $("winMax").addEventListener("click", () => window.runtime.WindowToggleMaximise());
    $("winClose").addEventListener("click", () => window.runtime.WindowHide());
    $("sideAccountBtn").addEventListener("click", () => {
        uiIsOpen($("sideMenu")) ? closeSideMenu() : openSideMenu();
    });
    [$("tocBtn"), $("favBtn")].forEach((btn) => {
        btn.addEventListener("mousedown", (e) => e.preventDefault());
    });
    let lastHeaderDown = { t: 0, x: 0, y: 0 };
    let headerPress = null;
    document.addEventListener("contextmenu", (e) => {
        if (e.target.closest("input, textarea, [contenteditable]")) return;
        e.preventDefault();
    });
    const tabStrip = $("docTabs");
    bindContextMenu(tabStrip, (event, tab) => {
        const oid = tab && tab.dataset.oid;
        const items = [];
        if (oid) {
            items.push({
                label: pinnedTabs.includes(oid) ? "Открепить вкладку" : "Закрепить вкладку",
                icon: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="m10.8 2.5 2.7 2.7-2 1.1-.9 3.1-1.3 1.3-2.1-2.1-3.1 3.1-.8-.8 3.1-3.1-2.1-2.1 1.3-1.3 3.1-.9z"/></svg>',
                action: () => togglePinnedTab(oid),
            });
            items.push({ type: "separator" });
            items.push({
                label: "Закрыть вкладку",
                icon: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="m4.5 4.5 7 7m0-7-7 7"/></svg>',
                action: () => closeTab(oid),
            });
            items.push({ type: "separator" });
        }
        items.push({
            label: "Закрыть все незакреплённые",
            icon: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3.5" width="9.5" height="10" rx="1.5"/><path d="M5.5 1.8h8a1 1 0 0 1 1 1v8"/></svg>',
            disabled: !openTabs.some((id) => !pinnedTabs.includes(id)),
            action: closeUnpinnedTabs,
        });
        return items;
    });
    tabStrip.addEventListener("wheel", (event) => {
        if (Math.abs(event.deltaY) > Math.abs(event.deltaX)) {
            tabStrip.scrollLeft += event.deltaY;
            event.preventDefault();
        }
    }, { passive: false });
    tabStrip.addEventListener("mousedown", (event) => {
        if (event.button !== 1) return;
        const tab = event.target.closest(".doc-tab");
        if (!tab || !tabStrip.contains(tab)) return;
        event.preventDefault();
        event.stopPropagation();
        closeTab(tab.dataset.oid);
    });
    document.querySelector(".header").addEventListener("mousedown", (e) => {
        if (e.button !== 0) return;
        if (window.wails && window.wails.flags && window.wails.flags.resizeEdge) return;
        if (e.target.closest("button, input, a, .search-box, .win-controls, .zoom-indicator")) {
            lastHeaderDown = { t: 0, x: 0, y: 0 };
            headerPress = null;
            return;
        }
        e.preventDefault();
        e.stopPropagation();
        closeMenu();
        const now = Date.now();
        const near = Math.abs(e.screenX - lastHeaderDown.x) <= 4 && Math.abs(e.screenY - lastHeaderDown.y) <= 4;
        if (now - lastHeaderDown.t < 500 && near) {
            lastHeaderDown = { t: 0, x: 0, y: 0 };
            headerPress = null;
            window.runtime.WindowToggleMaximise();
            return;
        }
        lastHeaderDown = { t: now, x: e.screenX, y: e.screenY };
        headerPress = { x: e.screenX, y: e.screenY };
    });
    document.addEventListener("mousemove", (e) => {
        if (!headerPress) return;
        if (e.buttons !== 1) { headerPress = null; return; }
        if (Math.abs(e.screenX - headerPress.x) > 4 || Math.abs(e.screenY - headerPress.y) > 4) {
            headerPress = null;
            lastHeaderDown = { t: 0, x: 0, y: 0 };
            window.WailsInvoke("drag");
        }
    });
    document.addEventListener("mouseup", () => { headerPress = null; });
    window.addEventListener("resize", updateMaxState);
    $("themeBtn").addEventListener("click", () => {
        applyTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark");
    });
    $("collapseAllBtn").addEventListener("click", () => {
        if (!collections.length) return;

        const shouldCollapse = collections.some((col) => !collapsedCollections.has(col.id));
        collapsedCollections = shouldCollapse
            ? new Set(collections.map((col) => col.id))
            : new Set();

        // The global collapse action also resets the document tree.
        // Individual collection toggles intentionally preserve expanded nodes.
        if (shouldCollapse) {
            expanded.clear();
            saveExpanded();
        }

        saveCollapsedCollections();
        const tree = $("docTree");
        const scroll = tree.scrollTop;
        renderTree();
        tree.scrollTop = scroll;
    });
    $("closeNotification").addEventListener("click", hideNotice);
    const toast = $("toast");
    toast.addEventListener("mouseenter", () => clearTimeout(toastTimer));
    toast.addEventListener("mouseleave", () => {
        if (toast.style.display !== "none") scheduleToastHide(toastDuration);
    });
    toast.addEventListener("click", () => {
        if (!toastAction) return;
        const action = toastAction;
        hideToast();
        action();
    });
    $("lockBtn").addEventListener("click", unlockDB);
    $("lockReset").addEventListener("click", resetFromLock);
    [$("lockPass"), $("lockPass2")].forEach((el) => {
        el.addEventListener("keydown", (e) => { if (e.key === "Enter") unlockDB(); });
    });

    // меню "три точки"
    $("menuBtn").addEventListener("click", () => {
        uiIsOpen($("docMenu")) ? closeMenu() : openMenu();
    });
    document.addEventListener("mousedown", (e) => {
        if (!uiIsOpen($("docMenu"))) return;
        if ($("docMenu").contains(e.target) || $("menuBtn").contains(e.target)) return;
        closeMenu();
    });
    document.addEventListener("mousedown", (e) => {
        if (!uiIsOpen($("sideMenu"))) return;
        if ($("sideMenu").contains(e.target) || $("sideAccountBtn").contains(e.target)) return;
        closeSideMenu();
    });
    $("menuFind").addEventListener("click", () => {
        if ($("menuFind").classList.contains("disabled")) return;
        closeMenu();
        openFindBar();
    });
    $("menuFullWidth").addEventListener("click", () => { applyFullWidth(!fullWidth); });
    $("menuCapture").addEventListener("click", toggleCapture);
    $("menuLock").addEventListener("click", () => { closeSideMenu(); lockNow(); });
    $("menuReset").addEventListener("click", () => { closeSideMenu(); openResetModal(); });

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

    $("settingsBtn").addEventListener("click", openSettings);
    $("settingsClose").addEventListener("click", closeSettings);
    $("settingsScreen").addEventListener("mousedown", (e) => {
        if (e.target === $("settingsScreen")) closeSettings();
    });
    document.querySelectorAll(".settings-nav-item").forEach((btn) => {
        btn.addEventListener("click", () => switchSettingsTab(btn.dataset.tab));
    });
    $("settingsSyncNow").addEventListener("click", async () => {
        await syncNow();
        updateSettingsSyncStatus();
    });
    $("settingsSyncFull").addEventListener("click", async () => {
        if (!confirm("Выполнить полную пересинхронизацию?")) return;
        await syncFullNow();
        updateSettingsSyncStatus();
    });
    $("settingsConnSave").addEventListener("click", saveSettingsConnection);
    $("settingsThemeToggle").addEventListener("click", () => {
        const newTheme = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
        applyTheme(newTheme);
        updateSettingsThemeToggle();
    });
    $("settingsFullWidth").addEventListener("click", () => {
        applyFullWidth(!fullWidth);
        updateSettingsFullWidth();
    });
    $("settingsZoomIn").addEventListener("click", () => { zoomIn(); updateSettingsZoom(); });
    $("settingsZoomOut").addEventListener("click", () => { zoomOut(); updateSettingsZoom(); });
    $("settingsZoomReset").addEventListener("click", () => { zoomReset(); updateSettingsZoom(); });
    $("settingsCaptureToggle").addEventListener("click", toggleCapture);
    $("settingsLock").addEventListener("click", () => {
        closeSettings();
        lockNow();
    });
    $("settingsCheckUpdate").addEventListener("click", checkForUpdates);
    $("settingsAboutRepo").addEventListener("click", (e) => {
        e.preventDefault();
        window.runtime.BrowserOpenURL("https://github.com/crazykivi/outline-reader");
    });
    $("settingsReset").addEventListener("click", () => {
        if (!confirm("Очистить локальную базу? Это действие нельзя отменить.")) return;
        closeSettings();
        doReset();
    });

    // глобальный поиск
    $("searchInput").addEventListener("input", () => {
        clearTimeout(searchTimer);
        const q = $("searchInput").value.trim();
        if (q.length < 2) { renderTree(); return; }
        searchTimer = setTimeout(() => runSearch(q), 250);
    });
    $("searchInput").addEventListener("keydown", (e) => {
        if (e.key === "Enter") {
            e.preventDefault();
            clearTimeout(searchTimer);
            const q = $("searchInput").value.trim();
            if (q.length < 2) renderTree();
            else runSearch(q);
        }
        if (e.key === "Escape") {
            e.stopPropagation();
            $("searchInput").value = "";
            clearTimeout(searchTimer);
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
            if (uiIsOpen($("docMenu"))) { closeMenu(); return; }
            if (uiIsOpen($("sideMenu"))) { closeSideMenu(); return; }
            if (uiIsOpen($("connModal"))) { closeConnModal(); return; }
            if (uiIsOpen($("resetModal"))) { closeResetModal(); return; }
            if (uiIsOpen($("aboutModal"))) { closeAboutModal(); return; }
            if (settingsOpen) { closeSettings(); return; }
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
            if ($("mainScreen").style.display !== "none") pingActivity();
        }, { passive: true }));

    $("docLink").addEventListener("click", (e) => {
        e.preventDefault();
        if (currentDocUrl) window.runtime.BrowserOpenURL(currentDocUrl);
    });
    $("favBtn").addEventListener("click", toggleFav);
    $("splitViewBtn").addEventListener("click", toggleSplitView);
    $("splitClose").addEventListener("click", closeSplitView);
    $("docTree").addEventListener("contextmenu", (event) => {
        const row = event.target.closest(".tree-row[data-oid]");
        if (!row || !$("docTree").contains(row)) return;
        event.preventDefault();
        event.stopPropagation();
        const oid = row.dataset.oid;
        showContextMenu(event, [{
            label: "Открыть справа",
            icon: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4"><rect x="1.5" y="2" width="13" height="12" rx="1.5"/><path d="M8 2v12"/></svg>',
            action: () => openSplitDocument(oid),
        }]);
    });
    $("splitContent").addEventListener("click", (event) => onContentClick(event, "split"));
    $("menuZoomIn").addEventListener("click", () => { zoomIn(); });
    $("menuZoomOut").addEventListener("click", () => { zoomOut(); });
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
        const needPass = true;
        $("lockSubtitle").textContent = st.legacy
            ? "Кэш старого формата (без мастер-пароля). Задайте мастер-пароль — документы будут зашифрованы на месте, без повторной синхронизации."
            : creating
                ? "Придумайте мастер-пароль (минимум 8 символов). Сменить его можно будет только очистив базу."
                : "Введите мастер-пароль";
        $("lockPass").style.display = needPass ? "block" : "none";
        $("lockPass2").style.display = (creating || st.legacy) ? "block" : "none";
        $("lockBtn").textContent = st.legacy ? "Зашифровать и открыть" : creating ? "Создать базу" : "Открыть";
        $("lockReset").style.display = creating ? "none" : "block";
        $("lockError").textContent = "";
        $("setupScreen").style.display = "none";
        $("mainScreen").style.display = "none";
        $("lockScreen").style.display = "flex";
        if (needPass) $("lockPass").focus();
        go().UnlockRetryAfter().then(startGuardCountdown).catch(() => { });
        return;
    }
    $("lockScreen").style.display = "none";
    startGuardCountdown(0);
    const s = await go().GetSettings();
    if (s && s.apiURL && s.hasKey) {
        cachedApiURL = s.apiURL;
        $("apiUrl").value = s.apiURL;
        await enterMain();
    } else {
            $("setupError").textContent = s && s.keyLost
                ? "Сохранённый API-ключ больше недоступен (защита Windows DPAPI). Введите ключ заново."
                : "";
            if (s && s.apiURL) $("apiUrl").value = s.apiURL;
        $("setupScreen").style.display = "flex";
    }
}

async function unlockDB() {
    if (guardLeft > 0) return;
    const passVisible = $("lockPass").style.display !== "none";
    const pass = passVisible ? $("lockPass").value : "";
    $("lockError").textContent = "";
    if ((!lockHasDB || lockLegacy) && pass !== $("lockPass2").value) {
        $("lockError").textContent = "Пароли не совпадают";
        $("lockPass").value = "";
        $("lockPass2").value = "";
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
        $("lockPass").value = "";
        $("lockPass2").value = "";
        $("lockError").textContent = String(err);
        startGuardCountdown(await go().UnlockRetryAfter());
    } finally {
        $("lockPass").value = "";
        $("lockPass2").value = "";
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

function clearMainUI() {
    prefsReady = false;
    prefsDirty.clear();
    clearTimeout(prefsTimer);
    currentOID = null;
    currentDocUpdated = "";
    currentDocUrl = "";
    history = []; historyIndex = -1; updateHistoryButtons();
    $("docContent").innerHTML = ""; $("docTitle").textContent = "";
    renderTabs();
    $("docDate").textContent = ""; $("docCrumbs").innerHTML = "";
    $("docLink").style.display = "none";
    $("favBtn").style.display = "none";
    $("docTree").innerHTML = ""; collections = []; index = {}; favorites = [];
    $("searchInput").value = "";
    $("tocPanel").style.display = "none";
    $("tocContent").innerHTML = "";
    closeFindBar(true); closeMenu(true); closeSideMenu(true); closePalette(true);
    settingsOpen = false;
    uiHideNow($("settingsScreen"));
    uiHideNow($("connModal"));
    uiHideNow($("resetModal"));
    uiHideNow($("aboutModal"));
    clearTimeout(toastTimer); toastAction = null; $("toast").classList.remove("toast-actionable"); uiHideNow($("toast"));
    clearTimeout(noticeTimer); uiHideNow($("updateNotification"));
    $("docViewer").style.display = "none";
    $("welcomeScreen").style.display = "none";
    $("mainScreen").style.display = "none";
    clearTimeout(searchTimer);
}

async function lockNow() {
    await flushPrefs();
    if ($("mainScreen").style.display === "none") return;
    try { await go().LockApp(); } catch (e) { }
    clearMainUI();
    await bootstrap(false);
}

function pingActivity() {
    const now = Date.now();
    if (now - lastActivitySent < 10000) return;
    lastActivitySent = now;
    go().NotifyActivity().catch(() => { });
}

function updateMaxState() {
    window.runtime.WindowIsMaximised().then((m) => {
        document.body.classList.toggle("maximised", !!m);
        $("winMax").title = m ? "Свернуть в окно" : "Развернуть";
    }).catch(() => { });
}

/* ---------- Экраны / подключение ---------- */

async function enterMain() {
    await loadPrefs();
    $("setupScreen").style.display = "none";
    $("lockScreen").style.display = "none";
    $("mainScreen").style.display = "flex";
    loadTree();
    go().NotifyActivity().catch(() => { });
    initCapture();
    if (!updateCheckStarted) {
        updateCheckStarted = true;
        void checkForUpdates({ notify: true });
    }
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
        cachedApiURL = url;
        $("apiKey").value = "";
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
    uiShow($("aboutModal"), "flex");
}

function openMenu() {
    closeSideMenu();
    const noDoc = $("docViewer").style.display === "none";
    $("menuFind").classList.toggle("disabled", noDoc);
    const menu = $("docMenu");
    uiShow(menu, "block");
    const r = $("menuBtn").getBoundingClientRect();
    const mh = menu.offsetHeight;
    let top = r.bottom + 8;
    if (top + mh > window.innerHeight - 8) {
        top = Math.max(8, r.top - mh - 8);
    }
    menu.style.top = top + "px";
    menu.style.left = "auto";
    menu.style.right = "20px";
}

function closeMenu(instant) {
    if (instant) uiHideNow($("docMenu"));
    else uiHide($("docMenu"));
}

function ensureContextMenu() {
    if (contextMenuElement) return contextMenuElement;
    const menu = document.createElement("div");
    menu.className = "context-menu";
    menu.setAttribute("role", "menu");
    menu.style.display = "none";
    document.body.appendChild(menu);
    document.addEventListener("mousedown", (event) => {
        if (contextMenuElement && !contextMenuElement.contains(event.target)) hideContextMenu();
    });
    document.addEventListener("keydown", (event) => {
        if (event.key === "Escape") hideContextMenu();
    });
    document.addEventListener("scroll", hideContextMenu, true);
    window.addEventListener("blur", hideContextMenu);
    contextMenuElement = menu;
    return menu;
}

function showContextMenu(event, items) {
    closeMenu();
    closeSideMenu();
    const menu = ensureContextMenu();
    menu.replaceChildren();
    items.forEach((item) => {
        if (item.type === "separator") {
            const separator = document.createElement("div");
            separator.className = "context-menu-separator";
            menu.appendChild(separator);
            return;
        }
        const button = document.createElement("button");
        button.type = "button";
        button.className = "context-menu-item" + (item.danger ? " danger" : "");
        button.setAttribute("role", "menuitem");
        button.disabled = !!item.disabled;
        if (item.icon) {
            const icon = document.createElement("span");
            icon.className = "context-menu-icon";
            icon.innerHTML = item.icon;
            button.appendChild(icon);
        }
        const label = document.createElement("span");
        label.className = "context-menu-label";
        label.textContent = item.label;
        button.appendChild(label);
        if (item.shortcut) {
            const shortcut = document.createElement("span");
            shortcut.className = "context-menu-shortcut";
            shortcut.textContent = item.shortcut;
            button.appendChild(shortcut);
        }
        button.addEventListener("click", () => {
            hideContextMenu();
            if (!item.disabled && typeof item.action === "function") item.action();
        });
        menu.appendChild(button);
    });
    if (!menu.children.length) return;
    menu.style.display = "block";
    const rect = menu.getBoundingClientRect();
    const left = Math.max(8, Math.min(event.clientX, window.innerWidth - rect.width - 8));
    const top = Math.max(8, Math.min(event.clientY, window.innerHeight - rect.height - 8));
    menu.style.left = left + "px";
    menu.style.top = top + "px";
}

function hideContextMenu() {
    if (contextMenuElement) contextMenuElement.style.display = "none";
}

function bindContextMenu(target, getItems) {
    if (!target) return;
    target.addEventListener("contextmenu", (event) => {
        if (event.target.closest("input, textarea, [contenteditable]")) return;
        const tab = event.target.closest(".doc-tab");
        const items = getItems(event, tab && target.contains(tab) ? tab : null);
        if (!items || !items.length) return;
        event.preventDefault();
        event.stopPropagation();
        showContextMenu(event, items);
    });
}

function openSideMenu() {
    closeMenu();
    $("fwSwitch").classList.toggle("on", fullWidth);
    $("capSwitch").classList.toggle("on", captureProtect);
    uiShow($("sideMenu"), "block");
    $("sideAccountBtn").setAttribute("aria-expanded", "true");
}

function closeSideMenu(instant) {
    $("sideAccountBtn").setAttribute("aria-expanded", "false");
    if (instant) uiHideNow($("sideMenu"));
    else uiHide($("sideMenu"));
}

window.addEventListener("resize", () => {
    closeMenu(true);
    closeSideMenu(true);
    hideContextMenu();
    closePalette(true);
    updateMaxState();
});

async function openConnModal() {
    $("connError").textContent = "";
    uiShow($("connModal"), "flex");
    connHasKey = false;
    try {
        const s = await go().GetSettings();
        $("connUrl").value = (s && s.apiURL) || "";
        connHasKey = !!(s && s.hasKey);
    } catch (err) { }
    $("connKey").value = "";
    $("connKey").placeholder = connHasKey ? "Ключ сохранён — пусто = не менять" : "ol_api_...";
    $("connUrl").focus();
}

function closeConnModal() { uiHide($("connModal")); }

async function saveConnection() {
    const url = $("connUrl").value.trim();
    const key = $("connKey").value.trim();
    $("connError").textContent = "";
    if (!url || (!key && !connHasKey)) { $("connError").textContent = "Заполните URL сервера и API-ключ"; return; }
    const btn = $("connSave");
    btn.disabled = true;
    try {
        btn.textContent = "Проверка...";
        await go().SetConnection(url, key);
        btn.textContent = "Синхронизация...";
        const res = await go().SyncDocuments();
        cachedApiURL = url;
        connHasKey = true;
        $("connKey").value = "";
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

function openResetModal() { uiShow($("resetModal"), "flex"); }
function closeResetModal() { uiHide($("resetModal")); }
function closeAboutModal() { uiHide($("aboutModal")); }

async function doReset() {
    const btn = $("resetConfirm");
    btn.disabled = true;
    btn.textContent = "Очистка...";
    try {
        clearTimeout(searchTimer);
        await go().ResetApp();
        ["or.expanded", "or.theme", "or.sideWidth", "or.fullWidth", "or.fontScale", "or.tocOpen", "or.captureProtect", "or.openTabs", "or.pinnedTabs", "or.activeTabOID"]
            .forEach((k) => localStorage.removeItem(k));
        window.location.reload();
    } catch (err) {
        showToast("Ошибка очистки", String(err), true);
        btn.disabled = false;
        btn.textContent = "Очистить";
    }
}

/* ---------- Защита от захвата экрана ---------- */

function renderCaptureState() {
    const sw = $("capSwitch");
    if (sw) sw.classList.toggle("on", captureProtect);
    const t = $("settingsCaptureToggle");
    if (t) t.classList.toggle("active", captureProtect);
}

async function initCapture() {
    const saved = prefGet("captureProtect");
    const on = saved === null ? isPortable : saved === "1";
    if (typeof go().SetCaptureProtection !== "function") {
        captureProtect = false;
        renderCaptureState();
        return;
    }
    try {
        const ok = await go().SetCaptureProtection(on);
        captureProtect = ok && on;
    } catch (err) {
        captureProtect = false;
        console.warn("[UI] initCapture:", err);
    }
    renderCaptureState();
}

async function toggleCapture() {
    if (captureBusy) return;
    if (typeof go().SetCaptureProtection !== "function") {
        showToast("Защита от захвата", "Доступна только на Windows", true);
        return;
    }
    captureBusy = true;
    const next = !captureProtect;
    try {
        const ok = await go().SetCaptureProtection(next);
        captureProtect = ok && next;
        prefSet("captureProtect", captureProtect ? "1" : "0");
        showToast(captureProtect ? "Защита от захвата включена" : "Защита от захвата выключена",
            ok ? "Окно не видно программам записи экрана" : "Не удалось применить (Windows 10 2004+)", !ok);
    } catch (err) {
        showToast("Ошибка", String(err), true);
    } finally {
        captureBusy = false;
        renderCaptureState();
    }
}

/* ---------- Буфер обмена ---------- */

function scheduleClipClear(text) {
    clearTimeout(clipTimer);
    clipTimer = setTimeout(async () => {
        try {
            const cur = await window.runtime.ClipboardGetText();
            if (cur === text) await window.runtime.ClipboardSetText("");
        } catch (e) { }
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
        renderTabs();
        await refreshFavorites();
        if (splitViewEnabled && splitDocumentOID) await restoreSplitDocument();
        renderSplitView();
        if (currentOID) expandAncestors(currentOID);
        renderTree();
        if (!tabsRestored) {
            tabsRestored = true;
            await restoreOpenTabs();
        }
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
                index[n.oid] = { title: n.title, parent, colName: col.name, collectionID: col.id };
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
    } catch (e) { }
}

function pluralDocs(n) {
    const m10 = n % 10, m100 = n % 100;
    if (m10 === 1 && m100 !== 11) return n + " документ";
    if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return n + " документа";
    return n + " документов";
}

function saveExpanded() { prefSet("expanded", JSON.stringify([...expanded])); }

function saveCollapsedCollections() {
    prefSet("collapsedCollections", JSON.stringify([...collapsedCollections]));
}

function expandAncestors(oid) {
    const item = index[oid];
    if (item && item.collectionID) collapsedCollections.delete(item.collectionID);
    let p = item ? item.parent : null;
    while (p) { expanded.add(p); p = (index[p] || {}).parent; }
    saveExpanded();
    saveCollapsedCollections();
}

function updateCollapseAllButton() {
    const btn = $("collapseAllBtn");
    if (!btn) return;

    const hasCollections = collections.length > 0;
    const willCollapse = hasCollections && collections.some((col) => !collapsedCollections.has(col.id));
    const action = willCollapse ? "collapse" : "expand";

    btn.disabled = !hasCollections;
    btn.dataset.action = action;
    btn.title = willCollapse
        ? "Свернуть все категории и вложенные документы"
        : "Развернуть все категории (документы останутся свёрнутыми)";
    btn.setAttribute("aria-label", btn.title);
}

function renderTree() {
    const el = $("docTree");
    el.innerHTML = "";
    if (!collections.length) {
        const d = document.createElement("div");
        d.className = "tree-empty";
        d.textContent = "База пуста. Нажмите кнопку синхронизации в меню.";
        el.appendChild(d);
        updateCollapseAllButton();
        return;
    }
    if (favorites.length) {
        const sec = document.createElement("section");
        const h = document.createElement("button");
        h.className = "collection-title";
        h.type = "button";
        h.setAttribute("aria-expanded", String(!favoritesCollapsed));
        h.innerHTML = `<span class="collection-name"></span><span class="collection-chevron" aria-hidden="true"></span>`;
        h.querySelector(".collection-name").textContent = "Избранное";
        h.addEventListener("click", toggleFavorites);
        sec.appendChild(h);
        if (!favoritesCollapsed) {
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
        }
        el.appendChild(sec);
    }
    for (const col of collections) {
        const sec = document.createElement("section");
        const h = document.createElement("button");
        h.className = "collection-title";
        h.type = "button";
        h.setAttribute("aria-expanded", String(!collapsedCollections.has(col.id)));
        h.innerHTML = `<span class="collection-name"></span><span class="collection-chevron" aria-hidden="true"></span>`;
        h.querySelector(".collection-name").textContent = col.name;
        h.addEventListener("click", () => toggleCollection(col.id));
        sec.appendChild(h);
        if (!collapsedCollections.has(col.id)) sec.appendChild(renderNodes(col.nodes, 0));
        el.appendChild(sec);
    }
    updateStatus();
    markActive();
    updateCollapseAllButton();
}

function toggleNode(oid) {
    expanded.has(oid) ? expanded.delete(oid) : expanded.add(oid);
    saveExpanded();
    const tree = $("docTree");
    const scroll = tree.scrollTop;
    renderTree();
    tree.scrollTop = scroll;
}

function saveFavoritesCollapsed() {
    prefSet("favoritesCollapsed", String(favoritesCollapsed));
}

function toggleFavorites() {
    favoritesCollapsed = !favoritesCollapsed;
    saveFavoritesCollapsed();
    const tree = $("docTree");
    const scroll = tree.scrollTop;
    renderTree();
    tree.scrollTop = scroll;
}

function toggleCollection(collectionID) {
    collapsedCollections.has(collectionID)
        ? collapsedCollections.delete(collectionID)
        : collapsedCollections.add(collectionID);
    saveCollapsedCollections();
    const tree = $("docTree");
    const scroll = tree.scrollTop;
    renderTree();
    tree.scrollTop = scroll;
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
    if (row) {
        row.classList.add("active");
        const tree = $("docTree");
        const treeRect = tree.getBoundingClientRect();
        const rowRect = row.getBoundingClientRect();
        if (rowRect.top < treeRect.top + 8) {
            tree.scrollBy({ top: rowRect.top - treeRect.top - 8, behavior: "smooth" });
        } else if (rowRect.bottom > treeRect.bottom - 8) {
            tree.scrollBy({ top: rowRect.bottom - treeRect.bottom + 8, behavior: "smooth" });
        }
    }
}

function goBack() {
    if (historyIndex <= 0) return;
    historyIndex--;
    skipHistoryPush = true;
    openDocument(history[historyIndex], undefined, "navigate", activeTabOID);
}

function goForward() {
    if (historyIndex >= history.length - 1) return;
    historyIndex++;
    skipHistoryPush = true;
    openDocument(history[historyIndex], undefined, "navigate", activeTabOID);
}

function updateHistoryButtons() {
    $("backBtn").disabled = historyIndex <= 0;
    $("forwardBtn").disabled = historyIndex >= history.length - 1;
}

function isEmptyTab(oid) {
    return typeof oid === "string" && oid.startsWith(EMPTY_TAB_PREFIX);
}

function getTabDocumentOID(tabID) {
    if (isEmptyTab(tabID)) return null;
    if (typeof tabID === "string" && tabID.startsWith(DUPLICATE_TAB_PREFIX)) {
        const separator = tabID.indexOf(":", DUPLICATE_TAB_PREFIX.length);
        if (separator < 0) return null;
        try { return decodeURIComponent(tabID.slice(separator + 1)); } catch (e) { return null; }
    }
    return tabID;
}

function createDuplicateTabID(oid) {
    let tabID;
    do {
        tabID = DUPLICATE_TAB_PREFIX + Date.now() + "_" + (++emptyTabSequence) + ":" + encodeURIComponent(oid);
    } while (openTabs.includes(tabID));
    return tabID;
}

function saveActiveTabScroll() {
    if (!activeTabOID || isEmptyTab(activeTabOID)) return;
    const viewer = document.querySelector(".viewer");
    if (viewer) tabScrollPositions.set(activeTabOID, viewer.scrollTop);
}

function createEmptyTabID() {
    let oid;
    do {
        oid = EMPTY_TAB_PREFIX + Date.now() + "_" + (++emptyTabSequence);
    } while (openTabs.includes(oid));
    return oid;
}

function activateEmptyTab(oid) {
    if (!isEmptyTab(oid) || !openTabs.includes(oid)) return;
    saveActiveTabScroll();
    openRequestID++;
    activeTabOID = oid;
    clearActiveDocument();
    saveOpenTabs();
    renderTabs();
}

function createNewTab() {
    saveActiveTabScroll();
    const oid = createEmptyTabID();
    openTabs.push(oid);
    activeTabOID = oid;
    clearActiveDocument();
    saveOpenTabs();
    renderTabs();
    const plus = $("newTabBtn");
    if (plus) plus.scrollIntoView({ block: "nearest", inline: "nearest" });
}

function normalizeTabOrder() {
    const pinned = new Set(pinnedTabs);
    openTabs = [
        ...openTabs.filter((id) => pinned.has(id)),
        ...openTabs.filter((id) => !pinned.has(id)),
    ];
}

function saveOpenTabs() {
    normalizeTabOrder();
    prefSet("openTabs", JSON.stringify(openTabs));
    prefSet("pinnedTabs", JSON.stringify(pinnedTabs));
    prefSet("activeTabOID", activeTabOID || "");
    void flushPrefs();
}

async function restoreOpenTabs() {
    normalizeTabOrder();
    renderTabs();
    const candidates = [activeTabOID, ...openTabs.slice().reverse()].filter((id, i, all) => id && all.indexOf(id) === i);
    for (const target of candidates) {
        if (isEmptyTab(target)) {
            if (target === activeTabOID) {
                activateEmptyTab(target);
                return;
            }
            continue;
        }
        const documentOID = getTabDocumentOID(target);
        if (!documentOID) continue;
        await openDocument(documentOID, undefined, "tab", target);
        if (currentOID === documentOID && activeTabOID === target) return;
        openTabs = openTabs.filter((id) => id !== target);
        pinnedTabs = pinnedTabs.filter((id) => id !== target);
    }
    activeTabOID = null;
    saveOpenTabs();
    renderTabs();
}

function renderTabs() {
    const bar = $("docTabs");
    if (!bar) return;
    normalizeTabOrder();
    bar.innerHTML = "";
    bar.style.display = openTabs.length ? "flex" : "none";
    for (const tabID of openTabs) {
        const empty = isEmptyTab(tabID);
        const documentOID = getTabDocumentOID(tabID);
        const info = empty ? {} : (index[documentOID] || {});
        const isPinned = pinnedTabs.includes(tabID);
        const tab = document.createElement("div");
        tab.className = "doc-tab" + (tabID === activeTabOID ? " active" : "") + (isPinned ? " pinned" : "");
        tab.dataset.oid = tabID;
        tab.draggable = true;
        tab.setAttribute("role", "tab");
        tab.setAttribute("aria-selected", String(tabID === activeTabOID));
        tab.tabIndex = tabID === activeTabOID ? 0 : -1;
        tab.title = empty ? "Новая вкладка" : (info.title || documentOID);

        if (isPinned) {
            const pin = document.createElement("span");
            pin.className = "doc-tab-pin";
            pin.innerHTML = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m16 3 5 5-4 1-4 4-1 5-3-3-5 5"/><path d="m14 5-4 4"/></svg>';
            tab.appendChild(pin);
        }

        const label = document.createElement("span");
        label.className = "doc-tab-title";
        label.textContent = empty ? "Новая вкладка" : (info.title || documentOID);
        tab.appendChild(label);

        if (!isPinned) {
            const close = document.createElement("button");
            close.type = "button";
            close.className = "doc-tab-close";
            close.title = "Закрыть вкладку";
            close.setAttribute("aria-label", "Закрыть " + (info.title || "документ"));
            close.innerHTML = '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M18 6 6 18M6 6l12 12"/></svg>';
            close.addEventListener("click", (event) => { event.stopPropagation(); closeTab(tabID); });
            tab.appendChild(close);
        }

        const activate = () => {
            if (empty) {
                if (tabID !== activeTabOID) activateEmptyTab(tabID);
            } else if (tabID !== activeTabOID) {
                openDocument(documentOID, undefined, "tab", tabID);
            }
        };
        tab.addEventListener("click", activate);
        tab.addEventListener("keydown", (event) => {
            if (event.key === "Enter" || event.key === " ") {
                event.preventDefault();
                activate();
            }
        });
        tab.addEventListener("dragstart", (event) => {
            draggedTabOID = tabID;
            event.dataTransfer.effectAllowed = "move";
            event.dataTransfer.setData("text/plain", tabID);
            tab.classList.add("dragging");
        });
        tab.addEventListener("dragover", (event) => {
            if (!draggedTabOID || draggedTabOID === tabID) return;
            if (pinnedTabs.includes(draggedTabOID) !== isPinned) return;
            event.preventDefault();
            event.dataTransfer.dropEffect = "move";
            const before = event.clientX < tab.getBoundingClientRect().left + tab.offsetWidth / 2;
            tab.classList.toggle("drop-before", before);
            tab.classList.toggle("drop-after", !before);
        });
        tab.addEventListener("dragleave", () => tab.classList.remove("drop-before", "drop-after"));
        tab.addEventListener("drop", (event) => {
            if (!draggedTabOID || pinnedTabs.includes(draggedTabOID) !== isPinned) return;
            event.preventDefault();
            const after = event.clientX >= tab.getBoundingClientRect().left + tab.offsetWidth / 2;
            reorderTab(draggedTabOID, tabID, after);
        });
        tab.addEventListener("dragend", () => {
            draggedTabOID = null;
            bar.querySelectorAll(".doc-tab").forEach((element) => element.classList.remove("dragging", "drop-before", "drop-after"));
        });
        bar.appendChild(tab);
        if (tabID === activeTabOID) tab.scrollIntoView({ block: "nearest", inline: "nearest" });
    }

    const add = document.createElement("button");
    add.type = "button";
    add.id = "newTabBtn";
    add.className = "doc-tab-add";
    add.title = "Новая вкладка";
    add.setAttribute("aria-label", "Открыть новую вкладку");
    add.textContent = "+";
    add.addEventListener("click", createNewTab);
    bar.appendChild(add);
}

function reorderTab(sourceOID, targetOID, after) {
    if (!sourceOID || sourceOID === targetOID) return;
    if (pinnedTabs.includes(sourceOID) !== pinnedTabs.includes(targetOID)) return;
    const next = openTabs.slice();
    const sourceIndex = next.indexOf(sourceOID);
    if (sourceIndex < 0) return;
    next.splice(sourceIndex, 1);
    const targetIndex = next.indexOf(targetOID);
    if (targetIndex < 0) return;
    next.splice(targetIndex + (after ? 1 : 0), 0, sourceOID);
    openTabs = next;
    draggedTabOID = null;
    saveOpenTabs();
    renderTabs();
}

function togglePinnedTab(oid) {
    if (!openTabs.includes(oid)) return;
    if (pinnedTabs.includes(oid)) pinnedTabs = pinnedTabs.filter((id) => id !== oid);
    else pinnedTabs.push(oid);
    normalizeTabOrder();
    saveOpenTabs();
    renderTabs();
}

function clearActiveDocument() {
    currentOID = null;
    currentDocUpdated = "";
    currentDocUrl = "";
    history = [];
    historyIndex = -1;
    updateHistoryButtons();
    $("docViewer").style.display = "none";
    $("welcomeScreen").style.display = "flex";
    $("tocBtn").disabled = true;
    $("favBtn").style.display = "none";
    $("docLink").style.display = "none";
    $("docCrumbs").innerHTML = "";
    $("docContent").innerHTML = "";
    $("docTitle").textContent = "";
    $("docDate").textContent = "";
    $("tocPanel").style.display = "none";
    $("tocContent").innerHTML = "";
    renderTree();
    closeFindBar(true);
}

function closeTab(oid) {
    if (activeTabOID === oid) openRequestID++;
    const pos = openTabs.indexOf(oid);
    if (pos < 0) return;
    openTabs.splice(pos, 1);
    pinnedTabs = pinnedTabs.filter((id) => id !== oid);
    tabHistories.delete(oid);
    tabScrollPositions.delete(oid);
    if (activeTabOID === oid) {
        activeTabOID = openTabs[Math.min(pos, openTabs.length - 1)] || null;
        if (activeTabOID) {
            saveOpenTabs();
            renderTabs();
            if (isEmptyTab(activeTabOID)) activateEmptyTab(activeTabOID);
            else openDocument(getTabDocumentOID(activeTabOID), undefined, "tab", activeTabOID);
            return;
        }
        clearActiveDocument();
    }
    saveOpenTabs();
    renderTabs();
}

function closeUnpinnedTabs() {
    const remaining = openTabs.filter((id) => pinnedTabs.includes(id));
    if (remaining.length === openTabs.length) return;
    const activeWasClosed = activeTabOID && !pinnedTabs.includes(activeTabOID);
    openTabs = remaining;
    if (activeWasClosed) {
        openRequestID++;
        activeTabOID = remaining[remaining.length - 1] || null;
        if (activeTabOID) {
            saveOpenTabs();
            renderTabs();
            if (isEmptyTab(activeTabOID)) activateEmptyTab(activeTabOID);
            else openDocument(getTabDocumentOID(activeTabOID), undefined, "tab", activeTabOID);
            return;
        }
        clearActiveDocument();
    }
    saveOpenTabs();
    renderTabs();
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
    uiShow($("palette"), "flex");
    $("paletteInput").value = "";
    $("paletteInput").focus();
    paletteDefault();
}

function closePalette(instant) {
    palette.open = false;
    if (instant) uiHideNow($("palette"));
    else uiHide($("palette"));
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
    } catch (e) { }
}

async function paletteSearch(q) {
    try {
        const res = await go().SearchDocuments(q);
        if ($("paletteInput").value.trim() !== q) return;
        palette.items = (res || []).slice(0, 12).map((d) => ({ doc: d, fav: false }));
        palette.sel = 0;
        renderPalette(q);
    } catch (e) { }
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

async function openDocument(oid, query, source, targetTabID) {
    const requestID = ++openRequestID;
    try {
        const doc = await go().GetDocument(oid);
        if (requestID !== openRequestID) return;
        activeSource = source === "fav" ? "fav" : "tree";

        const previousTabID = activeTabOID;
        const viewer = document.querySelector(".viewer");
        if (previousTabID && previousTabID !== targetTabID && !isEmptyTab(previousTabID) && viewer) {
            tabScrollPositions.set(previousTabID, viewer.scrollTop);
        }

        if (source === "tab") {
            targetTabID = targetTabID || oid;
            const saved = tabHistories.get(targetTabID);
            history = saved ? saved.history.slice() : [oid];
            historyIndex = saved ? saved.historyIndex : 0;
            skipHistoryPush = false;
            if (!openTabs.includes(targetTabID)) openTabs.push(targetTabID);
            activeTabOID = targetTabID;
        } else {
            const previousID = activeTabOID;
            let nextTabID = oid;
            if (source === "navigate" && previousID && !isEmptyTab(previousID)) {
                nextTabID = getTabDocumentOID(previousID) === oid ? previousID : createDuplicateTabID(oid);
            } else if (previousID && !isEmptyTab(previousID)) {
                if (getTabDocumentOID(previousID) === oid) nextTabID = previousID;
                else nextTabID = openTabs.some((id) => id !== previousID && getTabDocumentOID(id) === oid)
                    ? createDuplicateTabID(oid) : oid;
            } else if (openTabs.some((id) => id !== previousID && getTabDocumentOID(id) === oid)) {
                nextTabID = createDuplicateTabID(oid);
            }

            if (previousID && previousID !== nextTabID) {
                const saved = tabHistories.get(previousID) || { history: history.slice(), historyIndex };
                tabHistories.delete(previousID);
                tabScrollPositions.delete(previousID);
                tabHistories.set(nextTabID, saved);
            }
            const pos = openTabs.indexOf(previousID);
            if (pos >= 0) openTabs[pos] = nextTabID;
            else if (!openTabs.includes(nextTabID)) openTabs.push(nextTabID);
            if (pinnedTabs.includes(previousID)) pinnedTabs = pinnedTabs.map((id) => id === previousID ? nextTabID : id);
            activeTabOID = nextTabID;
            tabScrollPositions.delete(nextTabID);
        }

        normalizeTabOrder();
        saveOpenTabs();
        renderTabs();

        if (source !== "tab" && !skipHistoryPush) {
            if (historyIndex < history.length - 1) history = history.slice(0, historyIndex + 1);
            if (history[historyIndex] !== oid) {
                history.push(oid);
                historyIndex++;
            }
        }
        skipHistoryPush = false;
        if (activeTabOID) tabHistories.set(activeTabOID, { history: history.slice(), historyIndex });
        updateHistoryButtons();

        currentOID = oid;
        expandAncestors(oid);
        renderTree();
        showDoc(doc, cachedApiURL);
        if (source === "tab" && viewer) viewer.scrollTop = tabScrollPositions.get(activeTabOID) || 0;
        go().IsFavorite(oid).then(updateFavBtn).catch(() => { });
        if (query) openFindBar(query);
        else if (find.open) runFind();
        else { find.matches = []; find.current = -1; }
    } catch (err) {
        if (requestID === openRequestID) showToast("Ошибка", "Не удалось открыть документ: " + err, true);
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
    showDoc(doc, cachedApiURL);
    viewer.scrollTop = scroll;
    if (find.open) runFind();
    showToast("Документ обновлён", doc.Title || "", false);
}

function renderDocLink(doc, base) {
    const path = doc.Url || (doc.UrlID ? "/doc/" + doc.UrlID : "");
    currentDocUrl = base && path ? base.replace(/\/+$/, "") + path : "";
    const link = $("docLink");
    if (!currentDocUrl) {
        link.style.display = "none";
        return;
    }
    link.style.display = "inline-flex";
    link.href = currentDocUrl;
    link.title = "Открыть в Outline: " + currentDocUrl;
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
            a.addEventListener("click", () => openDocument(part.oid, undefined, "navigate"));
            el.appendChild(a);
        } else {
            const s = document.createElement("span");
            s.textContent = part.title;
            el.appendChild(s);
        }
    });
}

function parseMarkdownBlock(md) {
    return marked.parse(normalizeMarkdownTables(md));
}

function renderMarkdown(md, target) {
    const el = target || $("docContent");
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
    if (depth > 4) return parseMarkdownBlock(md);
    const lines = md.split("\n");
    const parts = [];
    let plain = [];
    let fence = null;
    let i = 0;
    const flush = () => {
        if (plain.length) { parts.push(parseMarkdownBlock(plain.join("\n"))); plain = []; }
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
    prefSet("tocOpen", tocOpen ? "1" : "0");
    const hasItems = $("tocContent").children.length > 0;
    const show = tocOpen && hasItems;
    $("tocPanel").style.display = show ? "block" : "none";
    $("tocBtn").classList.toggle("active", show);
    $("docViewer").classList.toggle("toc-open", show);
}

async function onContentClick(e, pane) {
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
                    if (pane === "split") await openSplitDocument(d.OutlineID);
                    else openDocument(d.OutlineID, undefined, "navigate");
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
    uiShow($("findBar"), "flex");
    if (prefill !== undefined) $("findInput").value = prefill;
    $("findInput").focus();
    $("findInput").select();
    runFind();
}

function closeFindBar(instant) {
    find.open = false;
    if (instant) uiHideNow($("findBar"));
    else uiHide($("findBar"));
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
    if (syncing) return;
    syncing = true;
    const btn = $("syncBtn"), side = $("sideSyncBtn");
    btn.classList.add("spin"); btn.disabled = true;
    side.classList.add("spin"); side.disabled = true;
    try {
        const res = await go().TrySyncDocuments();
        setNet(true);
        showToast("Синхронизация завершена", "Документов: " + res.total + " · " + fmtDelta(res), false);
        await loadTree();
        updateStatus();
        await refreshCurrentDoc();
    } catch (err) {
        const msg = String(err);
        if (!msg.includes("уже выполняется")) {
            setNet(false);
            showToast("Ошибка синхронизации", msg, true);
        }
    } finally {
        syncing = false;
        btn.classList.remove("spin"); btn.disabled = false;
        side.classList.remove("spin"); side.disabled = false;
    }
}

function bindBackendEvents() {
    window.runtime.EventsOn("update:progress", (progress) => {
        if (progress) renderUpdateProgress(progress);
    });
    window.runtime.EventsOn("app:locked", () => {
        if ($("mainScreen").style.display === "none") return;
        clearMainUI();
        bootstrap(false);
    });
    window.runtime.EventsOn("sync:state", async (st) => {
        if (!st) return;
        setNet(!!st.ok);
        if ($("mainScreen").style.display === "none" || !st.ok) return;
        const changed = (st.added || 0) + (st.updated || 0) + (st.deleted || 0) + (st.meta || 0);
        if (st.titles && st.titles.length) showUpdatedDocs(st.titles);
        if (changed > 0) {
            await loadTree();
            await refreshCurrentDoc();
        }
        updateStatus();
    });
}

function showUpdatedDocs(titles) {
    const list = $("updatedDocs");
    list.innerHTML = "";
    titles.forEach((t) => {
        const li = document.createElement("li");
        li.textContent = t;
        list.appendChild(li);
    });
    uiShow($("updateNotification"), "block");
    clearTimeout(noticeTimer);
    noticeTimer = setTimeout(hideNotice, 6000);
}

function hideNotice() {
    clearTimeout(noticeTimer);
    uiHide($("updateNotification"));
}

async function syncFullNow() {
    if (syncing) return;
    syncing = true;
    const btn = $("syncBtn"), side = $("sideSyncBtn");
    btn.classList.add("spin"); btn.disabled = true;
    side.classList.add("spin"); side.disabled = true;
    try {
        const res = await go().SyncDocumentsFull();
        setNet(true);
        showToast("Полная синхронизация завершена", "Документов: " + res.total + " · " + fmtDelta(res), false);
        await loadTree();
        updateStatus();
        await refreshCurrentDoc();
    } catch (err) {
        const msg = String(err);
        if (!msg.includes("уже выполняется")) {
            setNet(false);
            showToast("Ошибка синхронизации", msg, true);
        }
    } finally {
        syncing = false;
        btn.classList.remove("spin"); btn.disabled = false;
        side.classList.remove("spin"); side.disabled = false;
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

function showToast(title, msg, isErr, options = {}) {
    const t = $("toast");
    t.classList.toggle("error", !!isErr);
    $("toastTitle").textContent = title;
    $("toastMsg").textContent = msg;
    toastAction = typeof options.onClick === "function" ? options.onClick : null;
    toastDuration = options.duration || 4000;
    t.classList.toggle("toast-actionable", !!toastAction);
    t.title = toastAction ? "Открыть настройки обновлений" : "";
    uiShow(t, "block");
    clearTimeout(toastTimer);
    if (!t.matches(":hover")) scheduleToastHide(toastDuration);
}

function scheduleToastHide(duration) {
    clearTimeout(toastTimer);
    toastTimer = setTimeout(hideToast, duration);
}

function hideToast() {
    clearTimeout(toastTimer);
    toastAction = null;
    const t = $("toast");
    if (t) {
        t.classList.remove("toast-actionable");
        t.title = "";
        uiHide(t);
    }
}

async function openUpdateSettings() {
    openSettings();
    switchSettingsTab("updates");
    await checkForUpdates();
}

/* ---------- Тема / ширина / сайдбар ---------- */

function applyTheme(t) {
    document.documentElement.dataset.theme = t;
    prefSet("theme", t);
    const btn = $("themeBtn");
    const icon = btn.querySelector(".menu-icon");
    if (icon) {
        icon.innerHTML = t === "dark" ? ICON_SUN : ICON_MOON;
    } else {
        btn.innerHTML = t === "dark" ? ICON_SUN : ICON_MOON;
    }
    btn.title = t === "dark" ? "Светлая тема" : "Тёмная тема";
}

function applyFullWidth(v) {
    fullWidth = v;
    prefSet("fullWidth", v ? "1" : "0");
    $("docViewer").classList.toggle("full-width", v);
    $("fwSwitch").classList.toggle("on", v);
}

function applyFontScale(v) {
    fontScale = Math.round(v * 10) / 10;
    fontScale = Math.max(0.7, Math.min(2.0, fontScale));
    prefSet("fontScale", String(fontScale));
    document.documentElement.style.setProperty("--font-scale", String(fontScale));
    updateZoomIndicator();
}

function zoomIn() { applyFontScale(fontScale + 0.1); }
function zoomOut() { applyFontScale(fontScale - 0.1); }
function zoomReset() { applyFontScale(1); }

function updateZoomIndicator() {
    const el = $("zoomIndicator");
    if (!el) return;
    const pct = Math.round(fontScale * 100);
    if (pct === 100) {
        el.style.opacity = "0";
    } else {
        el.textContent = pct + "%";
        el.style.opacity = "1";
    }
}

function setSideWidth(w) {
    document.documentElement.style.setProperty("--side-w", w + "px");
}
function initResizer() {
    const sidebar = document.querySelector(".sidebar");
    const content = document.querySelector(".content");
    const edge = document.createElement("div");
    edge.className = "sidebar-edge-trigger";
    edge.setAttribute("aria-hidden", "true");
    content.prepend(edge);
    const saved = parseInt(prefGet("sideWidth", "300"), 10);
    if (saved >= 220 && saved <= 480) sidebar.style.width = saved + "px";
    setSideWidth(parseInt(sidebar.style.width, 10) || 300);

    let dragging = false;
    let hideTimer = null;
    const autoHide = prefGet("sidebarAutoHide", "0") === "1";
    const setVisible = (visible) => content.classList.toggle("sidebar-visible", visible);
    const scheduleHide = () => {
        clearTimeout(hideTimer);
        hideTimer = setTimeout(() => setVisible(false), 450);
    };
    const showSidebar = () => {
        clearTimeout(hideTimer);
        setVisible(true);
    };
    const setAutoHide = (enabled) => {
        clearTimeout(hideTimer);
        content.classList.toggle("sidebar-auto-hide", enabled);
        const button = $("sidebarAutoHideBtn");
        button.setAttribute("aria-pressed", String(enabled));
        button.setAttribute("aria-label", enabled ? "Закрепить боковую панель" : "Скрывать боковую панель");
        button.title = enabled ? "Закрепить боковую панель" : "Автоматически скрывать боковую панель";
        prefSet("sidebarAutoHide", enabled ? "1" : "0");
        setVisible(!enabled);
    };
    setAutoHide(autoHide);
    $("sidebarAutoHideBtn").addEventListener("click", () => {
        setAutoHide(!content.classList.contains("sidebar-auto-hide"));
    });
    sidebar.addEventListener("mouseenter", showSidebar);
    sidebar.addEventListener("mouseleave", scheduleHide);
    edge.addEventListener("mouseenter", showSidebar);
    edge.addEventListener("mouseleave", scheduleHide);
    sidebar.addEventListener("focusin", showSidebar);
    sidebar.addEventListener("focusout", (event) => {
        if (!sidebar.contains(event.relatedTarget)) scheduleHide();
    });

    $("sidebarResizer").addEventListener("mousedown", (e) => {
        if (content.classList.contains("sidebar-auto-hide")) return;
        closeSideMenu();
        dragging = true;
        document.body.classList.add("resizing");
        e.preventDefault();
    });
    document.addEventListener("mousemove", (e) => {
        if (!dragging) return;
        const w = Math.max(220, Math.min(480, e.clientX));
        sidebar.style.width = w + "px";
        setSideWidth(w);
    });
    document.addEventListener("mouseup", () => {
        if (!dragging) return;
        dragging = false;
        document.body.classList.remove("resizing");
        const w = parseInt(sidebar.style.width, 10) || 300;
        prefSet("sideWidth", w);
        setSideWidth(w);
    });
}

function openSettings() {
    closeMenu();
    closeSideMenu();
    settingsOpen = true;
    uiShow($("settingsScreen"), "flex");
    updateSettingsUI();
}

function closeSettings() {
    settingsOpen = false;
    uiHide($("settingsScreen"));
}

function switchSettingsTab(tabId) {
    document.querySelectorAll(".settings-tab").forEach((t) => t.classList.remove("active"));
    document.querySelectorAll(".settings-nav-item").forEach((b) => b.classList.remove("active"));
    $("tab-" + tabId).classList.add("active");
    document.querySelector(`[data-tab="${tabId}"]`).classList.add("active");
}

function updateSettingsUI() {
    updateSettingsSyncStatus();
    updateSettingsConnection();
    updateSettingsThemeToggle();
    updateSettingsFullWidth();
    updateSettingsZoom();
    updateSettingsCapture();
    updateSettingsAbout();
}

function updateSettingsSyncStatus() {
    // Получи статус синхронизации из бэкенда и обнови UI
    go().GetSyncStats().then((st) => {
        if (st.lastSync) {
            const d = new Date(st.lastSync);
            $("settingsSyncStatus").textContent = "Последняя синхронизация:";
            $("settingsSyncTime").textContent = d.toLocaleString("ru-RU");
        } else {
            $("settingsSyncStatus").textContent = "Не синхронизировано";
            $("settingsSyncTime").textContent = "";
        }
    });
}

function updateSettingsConnection() {
    go().GetSettings().then((s) => {
        $("settingsConnUrl").value = (s && s.apiURL) || "";
        $("settingsConnKey").value = "";
        $("settingsConnKey").placeholder = (s && s.hasKey) ? "Ключ сохранён" : "ol_api_...";
    });
}

async function saveSettingsConnection() {
    const url = $("settingsConnUrl").value.trim();
    const key = $("settingsConnKey").value.trim();
    $("settingsConnError").textContent = "";
    if (!url) { $("settingsConnError").textContent = "Заполните URL"; return; }
    const btn = $("settingsConnSave");
    btn.disabled = true;
    try {
        btn.textContent = "Сохранение...";
        await go().SetConnection(url, key);
        btn.textContent = "Синхронизация...";
        await go().SyncDocuments();
        showToast("Подключение обновлено", "", false);
        updateSettingsSyncStatus();
    } catch (err) {
        $("settingsConnError").textContent = String(err);
    } finally {
        btn.disabled = false;
        btn.textContent = "Сохранить";
    }
}

function updateSettingsThemeToggle() {
    const isDark = document.documentElement.dataset.theme === "dark";
    $("settingsThemeToggle").classList.toggle("active", isDark);
}

function updateSettingsFullWidth() {
    $("settingsFullWidth").classList.toggle("active", fullWidth);
}

function updateSettingsZoom() {
    const pct = Math.round(fontScale * 100);
    $("settingsZoomValue").textContent = pct + "%";
}

function updateSettingsCapture() {
    renderCaptureState();
}

function updateSettingsAbout() {
    go().AppVersion().then((v) => {
        const version = v === "dev" ? "dev-сборка" : "v" + v;
        $("settingsAboutVersion").textContent = version;
        $("settingsCurrentVersion").textContent = version;
        const nav = $("settingsNavVersion");
        if (nav) nav.textContent = v === "dev" ? "dev" : "v" + v;
    });
}

async function checkForUpdates({ notify = false } = {}) {
    const btn = $("settingsCheckUpdate");
    const status = $("settingsUpdateStatus");
    if (typeof go().CheckForUpdate !== "function") {
        console.warn("[UI] checkForUpdates: binding CheckForUpdate not found");
        status.textContent = "Проверка обновлений недоступна в этой сборке";
        return;
    }
    btn.disabled = true;
    btn.textContent = "Проверка...";
    status.textContent = "";
    status.className = "settings-note";
    try {
        const update = await go().CheckForUpdate();
        if (update && update.available) {
            if (notify) {
                showToast("Доступна новая версия", `v${update.version}. Нажмите, чтобы открыть настройки обновлений.`, false, {
                    duration: 8000,
                    onClick: openUpdateSettings,
                });
            }
            status.replaceChildren();
            status.append("Доступна новая версия: ");
            const version = document.createElement("strong");
            version.textContent = "v" + update.version;
            status.appendChild(version);
            status.appendChild(document.createElement("br"));
            const installButton = document.createElement("button");
            installButton.id = "settingsInstallUpdate";
            installButton.className = "btn-primary";
            installButton.style.marginTop = "8px";
            installButton.textContent = "Скачать и установить";
            installButton.addEventListener("click", () => installUpdate(update));
            status.appendChild(installButton);
            return update;
        } else {
            status.textContent = (update && update.note) || "У вас последняя версия";
            return update || null;
        }
    } catch (err) {
        status.textContent = "Ошибка: " + err;
        return null;
    } finally {
        btn.disabled = false;
        btn.textContent = "Проверить обновления";
    }
}

function formatUpdateSize(bytes) {
    if (!Number.isFinite(bytes) || bytes < 0) return "";
    const mb = bytes / (1024 * 1024);
    return mb >= 10 ? mb.toFixed(0) + " МБ" : mb.toFixed(1) + " МБ";
}

function renderUpdateProgress(progress) {
    const status = $("settingsUpdateStatus");
    const state = progress.state || "downloading";
    if (state === "error") {
        status.className = "settings-note update-error";
        status.textContent = "Не удалось установить обновление: " + (progress.message || "неизвестная ошибка");
        return;
    }

    status.className = "settings-note update-progress";
    status.replaceChildren();
    const labels = {
        downloading: "Загрузка обновления",
        verifying: "Проверка файла и контрольной суммы",
        installing: "Подготовка к установке",
        restarting: "Обновление готово — перезапуск приложения"
    };
    const label = document.createElement("div");
    label.className = "update-progress-label";
    const versionLabel = progress.version ? " · v" + progress.version : "";
    label.textContent = (labels[state] || "Обновление") + versionLabel;
    status.appendChild(label);

    if (state === "downloading") {
        const total = Number(progress.total) || 0;
        const downloaded = Number(progress.downloaded) || 0;
        const percent = total > 0 ? Math.max(0, Math.min(100, Number(progress.percent) || 0)) : 0;
        const meta = document.createElement("div");
        meta.className = "update-progress-meta";
        meta.textContent = total > 0
            ? formatUpdateSize(downloaded) + " из " + formatUpdateSize(total) + " · " + percent + "%"
            : formatUpdateSize(downloaded) + " загружено";
        status.appendChild(meta);

        const track = document.createElement("div");
        track.className = "update-progress-track";
        track.setAttribute("role", "progressbar");
        track.setAttribute("aria-label", "Загрузка обновления");
        if (total > 0) {
            track.setAttribute("aria-valuemin", "0");
            track.setAttribute("aria-valuemax", "100");
            track.setAttribute("aria-valuenow", String(percent));
        } else {
            track.setAttribute("aria-valuetext", "Загрузка");
            track.classList.add("indeterminate");
        }
        const fill = document.createElement("div");
        fill.className = "update-progress-fill";
        if (total > 0) fill.style.width = percent + "%";
        track.appendChild(fill);
        status.appendChild(track);
    } else {
        const meta = document.createElement("div");
        meta.className = "update-progress-meta";
        meta.textContent = state === "restarting" ? "Приложение сейчас закроется и запустит новую версию." : "Это займёт всего несколько секунд.";
        status.appendChild(meta);
        if (state !== "restarting") {
            const track = document.createElement("div");
            track.className = "update-progress-track indeterminate";
            track.setAttribute("role", "progressbar");
            track.setAttribute("aria-label", label.textContent);
            const fill = document.createElement("div");
            fill.className = "update-progress-fill";
            track.appendChild(fill);
            status.appendChild(track);
        }
    }
}

async function installUpdate(update) {
    const status = $("settingsUpdateStatus");
    const installButton = $("settingsInstallUpdate");
    if (typeof go().DownloadAndInstallUpdate !== "function") {
        console.warn("[UI] installUpdate: binding DownloadAndInstallUpdate not found");
        status.textContent = "Установка обновлений недоступна в этой сборке";
        return;
    }
    if (installButton) {
        installButton.disabled = true;
        installButton.textContent = "Подготовка...";
    }
    renderUpdateProgress({state: "downloading", downloaded: 0, total: 0, percent: 0, version: update.version});
    try {
        await go().DownloadAndInstallUpdate(update.downloadUrl);
        renderUpdateProgress({state: "restarting", version: update.version});
    } catch (err) {
        renderUpdateProgress({state: "error", message: String(err)});
        const retry = document.createElement("button");
        retry.className = "btn-primary btn-sm";
        retry.style.marginTop = "8px";
        retry.textContent = "Повторить установку";
        retry.addEventListener("click", () => installUpdate(update));
        status.appendChild(retry);
    }
}