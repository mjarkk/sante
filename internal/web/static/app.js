"use strict";

(() => {
  const root = document.documentElement;
  const content = document.getElementById("content");
  const tooltip = document.getElementById("tooltip");
  const tt = {
    title: tooltip.querySelector(".tt-title"),
    key: tooltip.querySelector(".tt-key"),
    label: tooltip.querySelector(".tt-label"),
    lines: tooltip.querySelectorAll(".tt-line"),
  };
  const levelLabels = {
    ok: "No downtime",
    minor: "Minor disruption",
    partial: "Partial outage",
    major: "Major outage",
    none: "No data",
  };
  const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");


  const theme = document.getElementById("theme");
  const themeButton = document.getElementById("theme-toggle");
  const themeMenu = document.getElementById("theme-menu");
  // Narrow windows get the modal: the button can wrap to the left edge there,
  // and the right-aligned popup would overflow.
  const usePopup = matchMedia("(hover: hover) and (pointer: fine) and (min-width: 33.75em)");
  function store(k, v) {
    try { v ? localStorage.setItem(k, v) : localStorage.removeItem(k); } catch { /* storage unavailable */ }
  }

  // Each radio group is named after the root data attribute it sets; the
  // empty value is the default and removes the attribute.
  for (const input of themeMenu.querySelectorAll("input")) {
    input.checked = input.value === (root.dataset[input.name] || "");
  }
  themeMenu.addEventListener("change", ({ target: { name, value } }) => {
    if (value) root.dataset[name] = value;
    else delete root.dataset[name];
    store(`sante-${name}`, value);
  });

  let pinned = false;
  let closeTimer;

  function openThemeMenu(pin) {
    clearTimeout(closeTimer);
    pinned ||= pin;
    themeButton.setAttribute("aria-expanded", "true");
    if (!usePopup.matches) {
      if (!themeMenu.open) themeMenu.showModal();
      return;
    }
    if (!themeMenu.open) {
      // Not show(): it moves focus into the popup, which a hover must not do.
      // Keyboard users reach the popup with Tab, as it follows the button.
      themeMenu.setAttribute("open", "");
      // Lays out the collapsed state for .expanded to grow from.
      void themeMenu.offsetWidth;
    }
    themeMenu.classList.add("expanded");
  }

  // The popup shrinks back into the button before it closes; opening it
  // again midway turns the shrink around.
  function closeThemeMenu() {
    clearTimeout(closeTimer);
    if (!themeMenu.classList.contains("expanded")) return;
    pinned = false;
    themeButton.setAttribute("aria-expanded", "false");
    themeMenu.classList.remove("expanded");
    Promise.allSettled(themeMenu.getAnimations({ subtree: true }).map((a) => a.finished)).then(() => {
      if (!themeMenu.classList.contains("expanded")) themeMenu.close();
    });
  }
  themeMenu.addEventListener("close", () => {
    pinned = false;
    themeButton.setAttribute("aria-expanded", "false");
  });

  themeButton.addEventListener("click", () => {
    if (pinned) closeThemeMenu();
    else openThemeMenu(true);
  });
  theme.addEventListener("pointerenter", (e) => {
    if (e.pointerType === "mouse" && usePopup.matches) openThemeMenu(false);
  });
  // Grace for a pointer that slips out for a moment.
  theme.addEventListener("pointerleave", (e) => {
    if (e.pointerType === "mouse" && !pinned) closeTimer = setTimeout(closeThemeMenu, 200);
  });
  theme.addEventListener("focusout", (e) => {
    if (e.relatedTarget && !theme.contains(e.relatedTarget)) closeThemeMenu();
  });
  document.addEventListener("pointerdown", (e) => {
    if (!theme.contains(e.target)) closeThemeMenu();
  });
  // While the popup expands, clicks outside its revealed part land on the
  // dialog too; only on a modal does that mean the backdrop.
  themeMenu.addEventListener("click", (e) => {
    const backdrop = e.target === themeMenu && themeMenu.matches(":modal");
    if (backdrop || e.target.closest(".theme-close")) themeMenu.close();
  });
  // A modal closes itself on Escape; the popup does not.
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || !themeMenu.classList.contains("expanded")) return;
    if (themeMenu.contains(document.activeElement)) themeButton.focus();
    closeThemeMenu();
  });


  // Mirrors shapeSpecs in funcs.go, which draws the first frame.
  const SHAPES = {
    circle: ["smooth", 0, 0],
    cookie9: ["scallop", 9, 0.07],
    cookie12: ["scallop", 12, 0.05],
    sunny: ["smooth", 8, 0.05],
    burst: ["burst", 12, 0.11],
    softburst: ["smooth", 10, 0.08],
    clover4: ["scallop", 4, 0.2],
    flower: ["scallop", 8, 0.12],
    puffy: ["smooth", 6, 0.07],
    square: ["squircle", 4, 0],
  };
  const radiiCache = {};

  function radii(name) {
    if (radiiCache[name]) return radiiCache[name];
    const [kind, n, a] = SHAPES[name] || SHAPES.circle;
    const out = [];
    for (let i = 0; i < 180; i++) {
      const t = (i / 180) * Math.PI * 2;
      let r = 1;
      if (kind === "smooth") r = 1 + a * Math.cos(n * t);
      else if (kind === "scallop") r = 1 + a * (2 * Math.abs(Math.cos((n * t) / 2)) - 1);
      else if (kind === "burst") r = 1 + a * (1 - 2 * Math.abs(Math.sin((n * t) / 2)));
      else if (kind === "squircle") r = (Math.abs(Math.cos(t)) ** n + Math.abs(Math.sin(t)) ** n) ** (-1 / n);
      out.push(r);
    }
    const peak = Math.max(...out);
    return (radiiCache[name] = out.map((r) => (r / peak) * 48));
  }

  function shapePath(r) {
    let d = "";
    for (let i = 0; i < r.length; i++) {
      const t = (i / r.length) * Math.PI * 2;
      d += `${i ? "L" : "M"}${(50 + r[i] * Math.cos(t)).toFixed(2)} ${(50 + r[i] * Math.sin(t)).toFixed(2)}`;
    }
    return `${d}Z`;
  }

  // Back-out easing: overshoots by about 13%, then settles.
  const spring = (x) => 1 + 3 * (x - 1) ** 3 + 2 * (x - 1) ** 2;

  let morphs = [];

  function initShapes(scope) {
    morphs = morphs.filter((m) => m.svg.isConnected);
    for (const el of scope.querySelectorAll(".mshape[data-shapes]")) {
      const [period, spin, turn] = el.dataset.motion.split(" ").map(Number);
      const svg = el.querySelector(".shape");
      const m = { svg, path: svg.firstElementChild, shapes: el.dataset.shapes.split(" ").map(radii), period, spin, turn, step: 0 };
      morphs.push(m);
      if (!reducedMotion.matches) drawShape(m, performance.now());
    }
  }

  // Every shape runs on the page clock, so one re-rendered by a refresh
  // carries on where the old one was.
  function drawShape(m, now) {
    const step = Math.floor(now / m.period);
    const k = spring(Math.min(1, Math.max(0, ((now % m.period) / m.period - 0.6) / 0.4)));
    if (k > 0 || step !== m.step) {
      const a = m.shapes[step % m.shapes.length];
      const b = m.shapes[(step + 1) % m.shapes.length];
      m.path.setAttribute("d", shapePath(k > 0 ? a.map((r, i) => r + (b[i] - r) * k) : a));
      m.step = step;
    }
    m.svg.style.transform = `rotate(${(now / 1000) * m.spin + (step + k) * m.turn}deg)`;
  }


  // M3 Expressive's wavy circular progress indicator.
  const every = Number(document.body.dataset.refresh) * 1000;
  let cycleStart = performance.now();

  function drawRing(now) {
    const track = content.querySelector(".ring-track");
    if (!track) return;
    const progress = track.nextElementSibling;
    const c = 22, width = 5, amp = 2, waves = 9;
    const r0 = c - amp - width / 2 - 1;
    const gap = (width + 3.5) / r0;
    const end = Math.max(0.001, Math.min(1, (now - cycleStart) / every)) * Math.PI * 2;
    const phase = reducedMotion.matches ? 0 : (now / 1000) * Math.PI * 2 * 0.3;
    const steps = Math.max(2, Math.ceil((end / (Math.PI * 2)) * 160));
    let d = "";
    for (let i = 0; i <= steps; i++) {
      const t = (i / steps) * end;
      // Ease the wave in and out so both ends of the stroke sit on the circle.
      const e = Math.min(1, t / 0.6, (end - t) / 0.6);
      const r = r0 + amp * e * e * (3 - 2 * e) * Math.sin(waves * t - phase);
      const a = t - Math.PI / 2;
      d += `${i ? "L" : "M"}${(c + r * Math.cos(a)).toFixed(2)} ${(c + r * Math.sin(a)).toFixed(2)}`;
    }
    progress.setAttribute("d", d);
    const t0 = end + gap - Math.PI / 2, t1 = Math.PI * 2 - gap - Math.PI / 2;
    track.setAttribute("d", t1 - t0 > 0.05
      ? `M${c + r0 * Math.cos(t0)} ${c + r0 * Math.sin(t0)}A${r0} ${r0} 0 ${t1 - t0 > Math.PI ? 1 : 0} 1 ${c + r0 * Math.cos(t1)} ${c + r0 * Math.sin(t1)}`
      : "");
  }

  function frame(now) {
    if (!reducedMotion.matches) for (const m of morphs) drawShape(m, now);
    if (every > 0) drawRing(now);
    requestAnimationFrame(frame);
  }


  // Sizes "Next refresh" so its length matches the height beside the hero.
  const fitObserver = new ResizeObserver(() => fitRefresh());

  function fitRefresh() {
    const box = content.querySelector(".refresh-box");
    const text = box?.firstElementChild;
    if (!text || getComputedStyle(text).writingMode.startsWith("horizontal")) return;
    const ems = text.offsetHeight / parseFloat(getComputedStyle(text).fontSize);
    const size = Math.max(12, Math.floor(box.clientHeight / ems));
    if (ems && Math.abs(size - parseFloat(getComputedStyle(box).getPropertyValue("--fit"))) >= 1) {
      box.style.setProperty("--fit", `${size}px`);
    }
  }

  function initRefresh() {
    fitObserver.disconnect();
    const box = content.querySelector(".refresh-box");
    if (box) fitObserver.observe(box);
    fitRefresh();
  }
  document.fonts?.ready.then(fitRefresh);


  let anchor = null;
  let activeWindow = null;

  function showTooltip(target, { title, level, label, lines }) {
    anchor = target;
    tt.title.textContent = title;
    tt.key.dataset.level = level;
    tt.label.textContent = label;
    tt.lines.forEach((el, i) => { el.textContent = lines[i] || ""; });
    tooltip.hidden = false;
  }

  // Centres the tooltip on x with its bottom edge at `above` or its top edge
  // at `below`, taking the preferred side unless the viewport has no room.
  // Offset sizes, not the bounding rect: the pop animation scales the latter.
  function place(x, above, below, preferBelow) {
    const w = tooltip.offsetWidth, h = tooltip.offsetHeight;
    const left = Math.max(12, Math.min(x - w / 2, window.innerWidth - w - 12));
    const down = preferBelow ? below + h <= window.innerHeight - 8 : above - h < 8;
    tooltip.style.left = `${left}px`;
    tooltip.style.top = `${down ? below : above - h}px`;
    tooltip.style.setProperty("--origin", `${x - left}px ${down ? "0%" : "100%"}`);
  }

  // The ::before hit area is as tall as the bar grows.
  function peakTop(bar) {
    return bar.getBoundingClientRect().bottom - parseFloat(getComputedStyle(bar, "::before").height);
  }

  // Clears the arrow or hand, which hangs about 20px below the hotspot.
  function placeAtPointer(e, bar) {
    place(e.clientX, peakTop(bar) - 10, e.clientY + 24, true);
  }

  function hideTooltip() {
    tooltip.hidden = true;
    anchor?.classList.remove("active");
    activeWindow?.classList.remove("active");
    anchor = activeWindow = null;
  }

  function showBar(bar) {
    if (anchor === bar) return;
    anchor?.classList.remove("active");
    bar.classList.add("active");
    const d = bar.dataset;
    showTooltip(bar, { title: d.date, level: d.level, label: levelLabels[d.level], lines: [d.summary, d.detail] });
  }

  document.addEventListener("pointerover", (e) => {
    const bar = e.target.closest?.(".bar");
    if (!bar) return;
    showBar(bar);
    placeAtPointer(e, bar);
  });
  // Only moves a tooltip already open, so one dismissed with Escape stays shut
  // until the pointer reaches another day.
  document.addEventListener("pointermove", (e) => {
    const bar = e.target.closest?.(".bar");
    if (bar && bar === anchor) placeAtPointer(e, bar);
  });
  document.addEventListener("pointerout", (e) => {
    if (e.target.closest?.(".bar") && !e.relatedTarget?.closest?.(".bar")) hideTooltip();
  });
  // A click focuses the bar it lands on; its tooltip stays at the pointer.
  document.addEventListener("focusin", (e) => {
    const bar = e.target;
    if (!bar.classList?.contains("bar") || bar === anchor) return;
    showBar(bar);
    const r = bar.getBoundingClientRect();
    place(r.left + r.width / 2, peakTop(bar) - 10, r.bottom + 10, true);
  });
  document.addEventListener("focusout", (e) => {
    if (e.target.classList?.contains("bar")) hideTooltip();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") hideTooltip();
  });
  window.addEventListener("scroll", () => { if (anchor) hideTooltip(); }, { passive: true });

  // The bars sit above the item's link so they keep their tooltips; a click
  // on them still opens the monitor.
  document.addEventListener("click", (e) => {
    e.target.closest?.(".item-link .bar")?.closest(".item").querySelector("a.item-head").click();
  });

  // One tab stop per bar strip; arrow keys move between visible days.
  document.addEventListener("keydown", (e) => {
    const bar = e.target.classList?.contains("bar") ? e.target : null;
    if (!bar) return;
    const bars = [...bar.parentElement.children].filter((b) => b.offsetParent !== null);
    const i = bars.indexOf(bar);
    const next = { ArrowLeft: i - 1, ArrowRight: i + 1, Home: 0, End: bars.length - 1 }[e.key];
    if (next === undefined || !bars[next]) return;
    e.preventDefault();
    bar.tabIndex = -1;
    bars[next].tabIndex = 0;
    bars[next].focus();
  });


  function ago(ms) {
    const s = Math.floor(ms / 1000);
    if (s < 5) return "just now";
    if (s < 60) return `${s}s ago`;
    if (s < 3600) return `${Math.floor(s / 60)}m ago`;
    if (s < 172800) return `${Math.floor(s / 3600)}h ago`;
    return `${Math.floor(s / 86400)}d ago`;
  }
  function tick() {
    const now = Date.now();
    for (const el of document.querySelectorAll("time[data-relative]")) {
      const t = Date.parse(el.getAttribute("datetime"));
      if (!Number.isNaN(t)) el.textContent = ago(now - t);
    }
  }
  setInterval(tick, 1000);


  function initCharts(scope) {
    for (const plot of scope.querySelectorAll(".chart-plot")) {
      const data = JSON.parse(plot.closest(".card").querySelector(".chart-data").textContent);
      const columns = plot.querySelector(".chart-bars");
      let index = -1;

      function show(i) {
        index = Math.max(0, Math.min(data.length - 1, i));
        const b = data[index];
        let label = "No checks";
        let level = "none";
        if (b.v >= 0) { label = formatMs(b.v); level = "line"; }
        else if (b.f > 0) { label = "All checks failed"; level = "major"; }
        const lines = [];
        if (b.n > 0) lines.push(`${b.n} check${b.n === 1 ? "" : "s"}, ${b.f} failed`);
        showTooltip(plot, { title: b.t, level, label, lines });
        activeWindow?.classList.remove("active");
        activeWindow = columns.children[index];
        activeWindow.classList.add("active");
        const col = activeWindow.getBoundingClientRect();
        const rect = plot.getBoundingClientRect();
        place(col.left + col.width / 2, rect.top - 10, rect.bottom + 10, false);
      }

      plot.addEventListener("pointermove", (e) => {
        const rect = columns.getBoundingClientRect();
        show(Math.floor(((e.clientX - rect.left) / rect.width) * data.length));
      });
      plot.addEventListener("pointerleave", hideTooltip);
      plot.addEventListener("blur", hideTooltip);
      plot.addEventListener("focus", () => show(index < 0 ? data.length - 1 : index));
      plot.addEventListener("keydown", (e) => {
        const step = { ArrowLeft: -1, ArrowRight: 1, PageUp: -6, PageDown: 6 }[e.key];
        if (e.key === "Home") show(0);
        else if (e.key === "End") show(data.length - 1);
        else if (step) show(index + step);
        else return;
        e.preventDefault();
      });
    }
  }

  function formatMs(v) {
    if (v >= 1000) return `${(v / 1000).toFixed(2)} s avg`;
    if (v < 10) return `${v.toFixed(1)} ms avg`;
    return `${Math.round(v)} ms avg`;
  }


  // A link to the JSON without scripts; with them, a toggle for the JSON
  // already on the page.
  const jsonToggle = document.getElementById("json-toggle");
  if (jsonToggle) {
    const json = document.getElementById(jsonToggle.getAttribute("aria-controls"));
    const label = jsonToggle.querySelector("span");
    jsonToggle.setAttribute("role", "button");
    jsonToggle.setAttribute("aria-expanded", "false");
    const toggle = (e) => {
      e.preventDefault();
      json.hidden = !json.hidden;
      jsonToggle.setAttribute("aria-expanded", String(!json.hidden));
      label.textContent = json.hidden ? "View as JSON" : "Hide JSON";
    };
    jsonToggle.addEventListener("click", toggle);
    jsonToggle.addEventListener("keydown", (e) => { if (e.key === " ") toggle(e); });
  }


  initShapes(document);
  initCharts(document);
  initRefresh();
  requestAnimationFrame(frame);


  // The old page stays on screen until the new HTML has arrived. A refresh is
  // skipped while a tooltip is open or focus is inside the page, so neither
  // pointer nor keyboard users lose their place.
  let stale = false;

  async function refresh() {
    if (document.hidden) { stale = true; return; }
    const focused = document.activeElement;
    if (anchor || (content.contains(focused) && focused !== content)) return;
    stale = false;
    content.setAttribute("aria-busy", "true");
    try {
      const res = await fetch(location.href, { cache: "no-store", headers: { Accept: "text/html" } });
      if (!res.ok) return;
      const doc = new DOMParser().parseFromString(await res.text(), "text/html");
      const fresh = doc.getElementById("content");
      if (!fresh || anchor) return;
      content.replaceChildren(...fresh.childNodes);
      initShapes(content);
      initCharts(content);
      initRefresh();
      tick();
    } catch {
      /* offline; the next interval retries */
    } finally {
      content.removeAttribute("aria-busy");
    }
  }

  if (every > 0) {
    setInterval(() => {
      cycleStart = performance.now();
      refresh();
    }, every);
    document.addEventListener("visibilitychange", () => { if (!document.hidden && stale) refresh(); });
  }
})();
