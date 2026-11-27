"use strict";

const UI_MOTION_MS = 200;
const UI_MOTION_PAD = 60;
const uiReduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)");

function uiIsOpen(el) {
    return !!el && el.dataset.open === "1";
}

function uiShow(el, display) {
    if (!el) return;
    if (el._uiHideTimer) {
        clearTimeout(el._uiHideTimer);
        el._uiHideTimer = null;
    }
    el.style.display = display || "block";
    void el.offsetWidth;
    el.dataset.open = "1";
    el.classList.add("is-open");
}

function uiHide(el, done) {
    if (!el) { if (done) done(); return; }
    if (!uiIsOpen(el)) { if (done) done(); return; }

    el.dataset.open = "0";
    el.classList.remove("is-open");

    const finish = () => {
        el._uiHideTimer = null;
        if (uiIsOpen(el)) return;
        el.style.display = "none";
        if (done) done();
    };

    if (uiReduceMotion.matches) { finish(); return; }
    el._uiHideTimer = setTimeout(finish, UI_MOTION_MS + UI_MOTION_PAD);
}

function uiHideNow(el) {
    if (!el) return;
    if (el._uiHideTimer) {
        clearTimeout(el._uiHideTimer);
        el._uiHideTimer = null;
    }
    el.dataset.open = "0";
    el.classList.remove("is-open");
    el.style.display = "none";
}