"use strict";

const $ = (id) => document.getElementById(id);
const go = () => window.go.main.App;

const ICON_SUN = '<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>';
const ICON_MOON = '<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/></svg>';

let collections = [];
let index = {};
let expanded = new Set();
let history = [];
let historyIndex = -1;
let skipHistoryPush = false;
let currentOID = null;
let updateTimer = null;
let searchTimer = null;
let toastTimer = null;
let fullWidth = localStorage.getItem("or.fullWidth") === "1";
let tocOpen = localStorage.getItem("or.tocOpen") === "1";
let fontScale = parseFloat(localStorage.getItem("or.fontScale") || "1");
let lastTreeClick = { oid: null, time: 0 };
let currentDocUrl = "";

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
        const s = await go().GetSettings();
        if (s && s.apiURL && s.apiKey) {
            $("apiUrl").value = s.apiURL;
            $("apiKey").value = s.apiKey;
            enterMain();
        }
    } catch (err) {
        console.error("[UI] settings:", err);
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
    $("menuFullWidth").addEventListener("click", () => {
        applyFullWidth(!fullWidth);
    });
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
    $("aboutModal").addEventListener("mousedown", (e) => {  if (e.target === $("aboutModal")) closeAboutModal();  });

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
        if (e.altKey && e.code === "ArrowLeft") {
            e.preventDefault();
            goBack();
            return;
        }
        if (e.altKey && e.code === "ArrowRight") {
            e.preventDefault();
            goForward();
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
    $("docLink").addEventListener("click", (e) => {
        e.preventDefault();
        if (currentDocUrl) window.runtime.BrowserOpenURL(currentDocUrl);
    });
    $("docLinkCopy").addEventListener("click", async () => {
        if (!currentDocUrl) return;
        const ok = await window.runtime.ClipboardSetText(currentDocUrl);
        showToast(ok ? "Ссылка скопирована" : "Ошибка", ok ? currentDocUrl : "Не удалось скопировать ссылку", !ok);
    });
    $("menuAbout").addEventListener("click", () => {
        closeMenu();
        showAbout();
    });
    $("menuZoomIn").addEventListener("click", () => { closeMenu(); zoomIn(); });
    $("menuZoomOut").addEventListener("click", () => { closeMenu(); zoomOut(); });
    $("zoomIndicator").addEventListener("click", zoomReset);
    $("menuZoomReset").addEventListener("click", () => { closeMenu(); zoomReset(); });
    $("docContent").addEventListener("click", onContentClick);
    $("backBtn").addEventListener("click", goBack);
    $("forwardBtn").addEventListener("click", goForward);
    $("tocBtn").addEventListener("click", () => applyTocOpen(!tocOpen));
}

/* ---------- Экраны / подключение ---------- */

function enterMain() {
    $("setupScreen").style.display = "none";
    $("mainScreen").style.display = "flex";
    loadTree();
    if (updateTimer) clearInterval(updateTimer);
    updateTimer = setInterval(checkUpdates, 60000);
    checkUpdates();
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
        const n = await go().SyncDocuments();
        setNet(true);
        closeConnModal();
        showToast("Подключение обновлено", "Документов в базе: " + n, false);
        await loadTree();
        updateLastSync();
        if (currentOID) openDocument(currentOID);
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
        clearTimeout(searchTimer);
        await go().ResetApp();

        ["or.expanded", "or.theme", "or.sideWidth", "or.fullWidth", "or.fontScale", "or.tocOpen"].forEach((k) => localStorage.removeItem(k));
        expanded.clear();
        collections = [];
        index = {};
        currentOID = null;
        history = [];
        historyIndex = -1;
        updateHistoryButtons();
        $("tocPanel").style.display = "none";
        $("tocContent").innerHTML = "";
        $("tocBtn").disabled = true;
        $("tocBtn").classList.remove("active");
        $("lastSync").textContent = "";
        $("lastSync").title = "";
        closeFindBar();
        $("searchInput").value = "";
        $("docViewer").style.display = "none";
        $("welcomeScreen").style.display = "flex";
        $("docTree").innerHTML = "";
        $("docCount").textContent = "";
        setNet(null);
        document.querySelector(".sidebar").style.width = "";
        applyTheme("dark");
        applyFullWidth(false);
        applyFontScale(1);
        tocOpen = false;
        closeResetModal();
        $("mainScreen").style.display = "none";
        $("setupScreen").style.display = "flex";
        $("apiUrl").value = "";
        $("apiKey").value = "";
        $("setupError").textContent = "";
    } catch (err) {
        showToast("Ошибка очистки", String(err), true);
    } finally {
        btn.disabled = false;
        btn.textContent = "Очистить";
    }
}

/* ---------- Дерево ---------- */

async function loadTree() {
    try {
        collections = await go().GetTree();
        buildIndex();
        updateDocCount();
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

function updateDocCount() {
    const n = Object.keys(index).length;
    $("docCount").textContent = n ? pluralDocs(n) : "";
}

async function updateLastSync() {
	const el = $("lastSync");
	if (!el) return;
	try {
		const iso = await go().GetLastSyncTime();
		if (!iso) { el.textContent = ""; el.title = ""; return; }
		const ago = Date.now() - new Date(iso).getTime();
		let text;
		if      (ago < 60_000)      text = "обновлено только что";
		else if (ago < 3_600_000)   text = "обновлено " + Math.floor(ago / 60_000) + " мин назад";
		else if (ago < 86_400_000)  text = "обновлено " + Math.floor(ago / 3_600_000) + " ч назад";
		else                        text = "обновлено " + Math.floor(ago / 86_400_000) + " дн назад";
		el.textContent = text;
		el.title = new Date(iso).toLocaleString("ru-RU");
	} catch (e) { el.textContent = ""; }
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
        d.textContent = "База пуста. Нажмите кнопку синхронизации в шапке.";
        el.appendChild(d);
        return;
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
    updateLastSync();
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
    const row = document.querySelector(`.tree-row[data-oid="${CSS.escape(currentOID)}"]`);
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

/* ---------- Документ ---------- */

async function openDocument(oid, query) {
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
    renderDocLink(doc, base);
    renderCrumbs(doc.OutlineID);
    renderMarkdown(doc.Text || "");
    renderTOC();
    document.querySelector(".viewer").scrollTop = 0;
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
    el.innerHTML = DOMPurify.sanitize(marked.parse(md), {
        ADD_TAGS: ["input"],
        ADD_ATTR: ["type", "checked", "disabled"],
    });
    el.querySelectorAll("img").forEach((img) => {
        const src = img.getAttribute("src") || "";
        if (src.includes("attachments.redirect")) {
            const id = new URL(src, "http://local").searchParams.get("id");
            if (id) img.src = "/img?id=" + encodeURIComponent(id);
        }
        img.loading = "lazy";
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
            const ok = await window.runtime.ClipboardSetText(pre.innerText);
            btn.textContent = ok ? "Скопировано" : "Ошибка";
            setTimeout(() => { btn.textContent = "Копировать"; }, 1500);
        });
        wrap.appendChild(btn);
    });
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
        window.runtime.ClipboardSetText(text).then((ok) => {
            showToast(ok ? "Скопировано" : "Ошибка", text.slice(0, 80), !ok);
        });
        return;
    }
    const a = e.target.closest("a");
    if (!a) return;
    const href = a.getAttribute("href") || "";

    if (href.startsWith("/doc/")) {
        e.preventDefault();
        const slug = href.split(/[?#]/)[0].replace(/\/+$/, "").split("/").pop();
        const urlId = slug.includes("-") ? slug.split("-").pop() : slug;
        try {
            const d = await go().GetDocumentByURLID(urlId);
            openDocument(d.OutlineID);
        } catch (err) {
            showToast("Документ не найден", "Его нет в локальной базе — выполните синхронизацию", true);
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

async function syncNow() {
    const btn = $("syncBtn");
    btn.classList.add("spin"); btn.disabled = true;
    try {
        const n = await go().SyncDocuments();
        setNet(true);
        showToast("Синхронизация завершена", "Документов в базе: " + n, false);
        await loadTree();
        updateLastSync();
        if (currentOID) openDocument(currentOID);
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
            updateLastSync();
            if (currentOID) openDocument(currentOID);
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