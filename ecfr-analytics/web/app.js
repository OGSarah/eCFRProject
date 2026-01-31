// API returns a same-origin path for API calls.
const API = (path) => path;

const metricLabels = {
  word_count: "Word count",
  churn: "Churn rate",
  readability: "Readability",
  words_per_chapter: "Words per chapter",
  checksum: "Checksum",
};

const metricsCache = new Map();
let topChart = null;
let tsChart = null;

const numberFmt = new Intl.NumberFormat("en-US");
const themeKey = "ecfr-theme";
const themeQuery = window.matchMedia("(prefers-color-scheme: dark)");

// jget performs a GET request and returns parsed JSON.
async function jget(path) {
  const res = await fetch(API(path));
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
  return res.json();
}

// jpost performs a POST request and returns parsed JSON.
async function jpost(path) {
  const res = await fetch(API(path), { method: "POST" });
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
  return res.json();
}

// fmtNumber formats numeric values with thousands separators.
function fmtNumber(value) {
  if (typeof value !== "number") return "--";
  return numberFmt.format(Math.round(value));
}

// fmtPercent formats a fractional value as a percentage.
function fmtPercent(value) {
  if (typeof value !== "number") return "--";
  return `${(value * 100).toFixed(1)}%`;
}

// fmtScore formats a numeric score with one decimal.
function fmtScore(value) {
  if (typeof value !== "number") return "--";
  return value.toFixed(1);
}

// fmtDateTime formats an ISO-ish timestamp into a local date/time string.
function fmtDateTime(value) {
  if (!value) return "--";
  const dt = new Date(value);
  if (Number.isNaN(dt.getTime())) return String(value);
  return dt.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

// formatValue picks a formatter based on metric type.
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

// setText updates an element's text content when present.
function setText(id, value) {
  const el = document.getElementById(id);
  if (el) el.textContent = value;
}

// loadLatest fetches and caches the latest rows for a metric.
async function loadLatest(metric) {
  if (metricsCache.has(metric)) return metricsCache.get(metric);
  const rows = await jget(`/api/metrics/latest?metric=${encodeURIComponent(metric)}`);
  metricsCache.set(metric, rows);
  return rows;
}

// loadAllLatest warms the cache for all primary metrics.
async function loadAllLatest() {
  await Promise.all([
    loadLatest("word_count"),
    loadLatest("churn"),
    loadLatest("readability"),
    loadLatest("words_per_chapter"),
    loadLatest("checksum"),
  ]);
}

// loadAgencies fetches agencies and populates the selector.
async function loadAgencies() {
  const agencies = await jget("/api/agencies");
  const sel = document.getElementById("agencySelect");
  sel.innerHTML = "";
  for (const a of agencies) {
    const opt = document.createElement("option");
    opt.value = a.slug;
    opt.textContent = a.name;
    sel.appendChild(opt);
  }
  setText("statAgencies", numberFmt.format(agencies.length));
}

// sumMetric totals numeric values in a metrics list.
function sumMetric(rows) {
  return rows.reduce((acc, r) => acc + (typeof r.value === "number" ? r.value : 0), 0);
}

// avgMetric averages numeric values in a metrics list.
function avgMetric(rows) {
  const nums = rows.filter((r) => typeof r.value === "number");
  if (!nums.length) return 0;
  return nums.reduce((acc, r) => acc + r.value, 0) / nums.length;
}

// latestDate returns the most recent date from a metrics list.
function latestDate(rows) {
  const dates = rows.map((r) => r.date).filter(Boolean).sort();
  return dates.length ? dates[dates.length - 1] : "--";
}

// formatHistoryStatus renders a compact availability label for historical metrics.
function formatHistoryStatus(status) {
  if (!status) return "Historical metrics: --";
  const offsets = String(status.history_offsets || "")
    .split(",")
    .map((v) => v.trim())
    .filter(Boolean);
  if (!offsets.length) return "Historical metrics: not available yet";
  const offsetsLabel = offsets.join("/");
  const asOf = status.history_last_refresh ? ` (as of ${fmtDateTime(status.history_last_refresh)})` : "";
  return `Historical metrics: ${offsetsLabel} days available${asOf}`;
}

// updateSummary refreshes headline stats and history status.
async function updateSummary() {
  const wcRows = await loadLatest("word_count");
  const churnRows = await loadLatest("churn");
  const readRows = await loadLatest("readability");
  const wpcRows = await loadLatest("words_per_chapter");
  let status = null;
  try {
    status = await jget("/api/status");
  } catch (e) {
  }

  setText("statTotalWords", fmtNumber(sumMetric(wcRows)));
  setText("statReadability", fmtScore(avgMetric(readRows)));
  setText("statLatestDate", latestDate(wcRows));
  setText("statChurn", fmtPercent(avgMetric(churnRows)));
  setText("statWordsPerChapter", fmtNumber(avgMetric(wpcRows)));
  setText("statCoverage", numberFmt.format(wcRows.length));
  setText("historyStatus", formatHistoryStatus(status));
}

// topN returns the top N numeric rows by value.
function topN(rows, n = 12) {
  return [...rows]
    .filter((r) => typeof r.value === "number")
    .sort((a, b) => b.value - a.value)
    .slice(0, n);
}

// bottomN returns the bottom N numeric rows by value.
function bottomN(rows, n = 5) {
  return [...rows]
    .filter((r) => typeof r.value === "number")
    .sort((a, b) => a.value - b.value)
    .slice(0, n);
}

// minDate returns the earliest date from a metrics list.
function minDate(rows) {
  const dates = rows.map((r) => r.date).filter(Boolean).sort();
  return dates.length ? dates[0] : "--";
}

// maxDate returns the latest date from a metrics list.
function maxDate(rows) {
  const dates = rows.map((r) => r.date).filter(Boolean).sort();
  return dates.length ? dates[dates.length - 1] : "--";
}

// chartColor selects the primary chart color per metric.
function chartColor(metric) {
  switch (metric) {
    case "churn":
      return "#f1b24a";
    case "readability":
      return "#3d9a7b";
    case "words_per_chapter":
      return "#1c6e8c";
    default:
      return cssVar("--ink") || "#0f1b2d";
  }
}

// cssVar reads a CSS custom property from the root element.
function cssVar(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// chartGridColor returns the gridline color for charts.
function chartGridColor() {
  return cssVar("--chart-grid") || "rgba(15, 27, 45, 0.08)";
}

// chartFill returns the area fill color for charts.
function chartFill() {
  return cssVar("--chart-fill") || "rgba(28, 110, 140, 0.15)";
}

// renderTopChart draws the top-agencies bar chart.
async function renderTopChart() {
  const metric = document.getElementById("topMetricSelect").value;
  const rows = await loadLatest(metric);
  const top = topN(rows, 12);
  const gridColor = chartGridColor();

  setText("topChartTitle", `Top agencies by ${metricLabels[metric] ?? metric}`);

  const cfg = {
    type: "bar",
    data: {
      labels: top.map((r) => r.name),
      datasets: [{
        label: metricLabels[metric] ?? metric,
        data: top.map((r) => r.value),
        backgroundColor: chartColor(metric),
      }],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: { legend: { display: false } },
      scales: {
        y: {
          grid: { color: gridColor },
          ticks: {
            callback: (val) => {
              if (metric === "churn") return `${(val * 100).toFixed(0)}%`;
              return numberFmt.format(val);
            },
          },
        },
        x: {
          grid: { color: gridColor },
          ticks: { autoSkip: false, maxRotation: 40, minRotation: 20 },
        },
      },
    },
  };

  if (topChart) topChart.destroy();
  topChart = new Chart(document.getElementById("topChart"), cfg);
}

// renderInsightList renders a simple list of labeled metric values.
function renderInsightList(el, rows, fmt) {
  el.innerHTML = "";
  for (const r of rows) {
    const li = document.createElement("li");
    li.className = "insight-item";
    li.innerHTML = `<strong>${escapeHtml(r.name)}</strong><span class="insight-value">${escapeHtml(fmt(r.value))}</span>`;
    el.appendChild(li);
  }
}

// renderInsights updates the insight cards and lists.
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

// renderGrowthHotspots populates the growth list from the API.
async function renderGrowthHotspots() {
  const list = document.getElementById("growthHotspots");
  if (!list) return;
  const rows = await jget("/api/insights/growth?days=365");
  const display = rows.map((r) => ({ name: r.agency, value: r.delta }));
  renderInsightList(list, display, fmtNumber);
}

// loadTimeseries fetches data and redraws the trendline chart.
async function loadTimeseries() {
  const slug = document.getElementById("agencySelect").value;
  if (!slug) return;

  const metric = document.getElementById("metricSelect").value;
  const days = document.getElementById("daysSelect").value;

  const rows = await jget(
    `/api/metrics/agency/${encodeURIComponent(slug)}/timeseries?metric=${encodeURIComponent(metric)}&days=${encodeURIComponent(days)}`
  );
  const data = [...rows].reverse();
  const gridColor = chartGridColor();

  const cfg = {
    type: "line",
    data: {
      labels: data.map((r) => r.date),
      datasets: [{
        label: metricLabels[metric] ?? metric,
        data: data.map((r) => r.value),
        borderColor: chartColor(metric),
        backgroundColor: chartFill(),
        fill: true,
        tension: 0.3,
      }],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: { legend: { display: true } },
      scales: {
        y: { grid: { color: gridColor } },
        x: { grid: { color: gridColor } },
      },
    },
  };

  if (tsChart) tsChart.destroy();
  tsChart = new Chart(document.getElementById("tsChart"), cfg);

  setText("agencyMeta", `slug: ${slug} • points: ${data.length}`);
  updateAgencyCards(slug);
}

// findMetricValue returns a cached metric value for a specific agency.
function findMetricValue(metric, slug) {
  const rows = metricsCache.get(metric) || [];
  const row = rows.find((r) => r.slug === slug);
  return row ? row.value : null;
}

// updateAgencyCards refreshes the per-agency stat tiles.
function updateAgencyCards(slug) {
  setText("agencyWordCount", formatValue("word_count", findMetricValue("word_count", slug)));
  setText("agencyChurn", formatValue("churn", findMetricValue("churn", slug)));
  setText("agencyReadability", formatValue("readability", findMetricValue("readability", slug)));
  setText("agencyWordsPerChapter", formatValue("words_per_chapter", findMetricValue("words_per_chapter", slug)));
}

// loadReviewTable renders the sortable review table.
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

// refresh triggers a server-side refresh and reloads UI data.
async function refresh() {
  try {
    const r = await jpost("/api/refresh");
    metricsCache.clear();
    await loadAllLatest();
    await updateSummary();
    await renderInsights();
    await renderGrowthHotspots();
    await renderTopChart();
    await loadTimeseries();
    await loadReviewTable();

  } catch (e) {
  }
}

// exportCsv downloads the latest metric set as CSV.
function exportCsv() {
  const metric = document.getElementById("reviewMetricSelect").value;
  const rows = metricsCache.get(metric) || [];
  const header = ["agency", "date", metric];
  const lines = [header.join(",")];

  for (const r of rows) {
    const value = r.value == null ? "" : String(r.value).replaceAll("\"", "\"\"");
    lines.push(`"${r.name}","${r.date}","${value}"`);
  }

  const blob = new Blob([lines.join("\n")], { type: "text/csv" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `ecfr-${metric}-latest.csv`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

// escapeHtml avoids HTML injection in rendered strings.
function escapeHtml(s) {
  return String(s)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

// applyTheme switches between light and dark theme styles.
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

  Chart.defaults.color = cssVar("--ink") || "#0f1b2d";
  Chart.defaults.borderColor = chartGridColor();
  Chart.defaults.font.family = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
}

// loadThemePreference returns the system theme preference.
function loadThemePreference() {
  return themeQuery.matches ? "dark" : "light";
}

// syncThemeFromSystem reapplies theme and redraws visuals.
function syncThemeFromSystem() {
  applyTheme(themeQuery.matches ? "dark" : "light");
  renderTopChart();
  loadTimeseries();
  renderInsights();
  renderGrowthHotspots();
}

// refreshFromServer reloads cached data and redraws UI panels.
async function refreshFromServer() {
  metricsCache.clear();
  await loadAllLatest();
  await updateSummary();
  await renderInsights();
  await renderGrowthHotspots();
  await renderTopChart();
  await loadReviewTable();
  await loadTimeseries();
}

document.getElementById("exportCsvBtn").addEventListener("click", exportCsv);
document.getElementById("topMetricSelect").addEventListener("change", renderTopChart);
document.getElementById("reviewMetricSelect").addEventListener("change", loadReviewTable);
document.getElementById("reviewSearch").addEventListener("input", loadReviewTable);
document.getElementById("agencySelect").addEventListener("change", loadTimeseries);
document.getElementById("metricSelect").addEventListener("change", loadTimeseries);
document.getElementById("daysSelect").addEventListener("change", loadTimeseries);
// init boots the UI, pulls data, and schedules refreshes.
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
