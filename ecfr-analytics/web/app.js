const API = (path) => path;

const metricsCache = new Map();

const numberFmt = new Intl.NumberFormat("en-US");
const themeKey = "ecfr-theme";
const themeQuery = window.matchMedia("(prefers-color-scheme: dark)");

async function jget(path) {
  const res = await fetch(API(path));
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
  return res.json();
}

async function jpost(path) {
  const res = await fetch(API(path), { method: "POST" });
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
  return res.json();
}

function fmtNumber(value) {
  if (typeof value !== "number") return "--";
  return numberFmt.format(Math.round(value));
}

function fmtPercent(value) {
  if (typeof value !== "number") return "--";
  return `${(value * 100).toFixed(1)}%`;
}

function fmtScore(value) {
  if (typeof value !== "number") return "--";
  return value.toFixed(1);
}

function formatValue(metric, value) {
  switch (metric) {
    case "word_count":
      return fmtNumber(value);
    case "words_per_chapter":
      return fmtNumber(value);
    case "churn":
      return fmtPercent(value);
    case "readability":
      return fmtScore(value);
    default:
      return String(value ?? "--");
  }
}

function setText(id, value) {
  const el = document.getElementById(id);
  if (el) el.textContent = value;
}

async function loadLatest(metric) {
  if (metricsCache.has(metric)) return metricsCache.get(metric);
  const rows = await jget(`/api/metrics/latest?metric=${encodeURIComponent(metric)}`);
  metricsCache.set(metric, rows);
  return rows;
}

async function loadAllLatest() {
  await Promise.all([
    loadLatest("word_count"),
    loadLatest("churn"),
    loadLatest("readability"),
    loadLatest("words_per_chapter"),
    loadLatest("checksum"),
  ]);
}

async function loadAgencies() {
  const agencies = await jget("/api/agencies");
  setText("statAgencies", numberFmt.format(agencies.length));
}

function sumMetric(rows) {
  return rows.reduce((acc, r) => acc + (typeof r.value === "number" ? r.value : 0), 0);
}

function avgMetric(rows) {
  const nums = rows.filter((r) => typeof r.value === "number");
  if (!nums.length) return 0;
  return nums.reduce((acc, r) => acc + r.value, 0) / nums.length;
}

function latestDate(rows) {
  const dates = rows.map((r) => r.date).filter(Boolean).sort();
  return dates.length ? dates[dates.length - 1] : "--";
}

async function updateSummary() {
  const wcRows = await loadLatest("word_count");
  const churnRows = await loadLatest("churn");
  const readRows = await loadLatest("readability");
  const wpcRows = await loadLatest("words_per_chapter");

  setText("statTotalWords", fmtNumber(sumMetric(wcRows)));
  setText("statReadability", fmtScore(avgMetric(readRows)));
  setText("statLatestDate", latestDate(wcRows));
  setText("statChurn", fmtPercent(avgMetric(churnRows)));
  setText("statWordsPerChapter", fmtNumber(avgMetric(wpcRows)));
  setText("statCoverage", numberFmt.format(wcRows.length));
}

function topN(rows, n = 12) {
  return [...rows]
    .filter((r) => typeof r.value === "number")
    .sort((a, b) => b.value - a.value)
    .slice(0, n);
}

function bottomN(rows, n = 5) {
  return [...rows]
    .filter((r) => typeof r.value === "number")
    .sort((a, b) => a.value - b.value)
    .slice(0, n);
}

function renderInsightList(el, rows, fmt) {
  el.innerHTML = "";
  for (const r of rows) {
    const li = document.createElement("li");
    li.className = "insight-item";
    li.innerHTML = `<strong>${escapeHtml(r.name)}</strong><span class="insight-value">${escapeHtml(fmt(r.value))}</span>`;
    el.appendChild(li);
  }
}

async function renderInsights() {
  const readRows = await loadLatest("readability");
  const wcRows = await loadLatest("word_count");
  const wpcRows = await loadLatest("words_per_chapter");

  const readList = document.getElementById("readabilityLow");
  if (readList) {
    const mostComplex = [...readRows]
      .filter((r) => typeof r.value === "number")
      .sort((a, b) => a.value - b.value)
      .slice(0, 5);
    renderInsightList(readList, mostComplex, fmtScore);
  }

  const densityList = document.getElementById("densityExtremes");
  if (densityList) {
    const highest = topN(wpcRows, 3);
    const lowest = bottomN(wpcRows, 3);
    renderInsightList(densityList, [...highest, ...lowest], fmtNumber);
  }
}


async function loadReviewTable() {
  const metric = document.getElementById("reviewMetricSelect").value;
  const search = document.getElementById("reviewSearch").value.trim().toLowerCase();
  const rows = await loadLatest(metric);

  let filtered = rows;
  if (search) {
    filtered = rows.filter((r) => r.name.toLowerCase().includes(search));
  }

  const numeric = filtered.filter((r) => typeof r.value === "number");
  const nonNumeric = filtered.filter((r) => typeof r.value !== "number");

  numeric.sort((a, b) => b.value - a.value);
  nonNumeric.sort((a, b) => a.name.localeCompare(b.name));

  const ordered = [...numeric, ...nonNumeric].slice(0, 200);

  const tbody = document.getElementById("reviewTable");
  tbody.innerHTML = "";
  for (const r of ordered) {
    const tr = document.createElement("tr");
    const isChecksum = metric === "checksum";
    const value = isChecksum
      ? `<code>${escapeHtml(String(r.value ?? ""))}</code>`
      : `<span class="highlight-green">${escapeHtml(formatValue(metric, r.value))}</span>`;
    tr.innerHTML = `<td>${escapeHtml(r.name)}</td><td>${escapeHtml(r.date)}</td><td>${value}</td>`;
    tbody.appendChild(tr);
  }
}

async function refresh() {
  try {
    const r = await jpost("/api/refresh");
    metricsCache.clear();
    await loadAllLatest();
    await updateSummary();
    await renderInsights();
    await loadReviewTable();

  } catch (e) {
  }
}

function escapeHtml(s) {
  return String(s)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function applyTheme(theme) {
  const root = document.documentElement;
  if (theme) {
    root.setAttribute("data-theme", theme);
  } else {
    root.removeAttribute("data-theme");
  }

  const toggle = document.getElementById("themeToggle");
  if (toggle) {
    const isDark = root.getAttribute("data-theme") === "dark";
    toggle.textContent = isDark ? "Light mode" : "Dark mode";
    toggle.setAttribute("aria-pressed", String(isDark));
  }

}

function loadThemePreference() {
  return themeQuery.matches ? "dark" : "light";
}

function syncThemeFromSystem() {
  applyTheme(themeQuery.matches ? "dark" : "light");
  renderInsights();
}

async function refreshFromServer() {
  metricsCache.clear();
  await loadAllLatest();
  await updateSummary();
  await renderInsights();
  await loadReviewTable();
}

document.getElementById("reviewMetricSelect").addEventListener("change", loadReviewTable);
document.getElementById("reviewSearch").addEventListener("input", loadReviewTable);

(async function init() {
  localStorage.removeItem(themeKey);
  applyTheme(loadThemePreference());
  themeQuery.addEventListener("change", syncThemeFromSystem);

  await loadAgencies();
  await refreshFromServer();
  setInterval(() => {
    refreshFromServer().catch(() => {});
  }, 300000);
})();
